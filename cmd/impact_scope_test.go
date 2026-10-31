package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
)

func impactGit(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null"}, args...)...) // #nosec G204 -- test-owned repository and fixed commands.
	command.Env = impactGitEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}

	return output
}

func impactGitEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}

	return env
}

func newImpactFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/impact\n\ngo 1.25.0\n")
	writeFixture(t, filepath.Join(dir, ".imp.yaml"), "{}\n")
	for _, pkg := range []string{"a", "b", "other"} {
		if err := os.Mkdir(filepath.Join(dir, pkg), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(t, filepath.Join(dir, "a", "code.go"), "package a\nfunc Value(a,b int) int {\n _ = a + 0\n return a + b\n}\nfunc Other(a,b int) int { return a - b }\n")
	writeFixture(t, filepath.Join(dir, "a", "code_test.go"), "package a\nimport \"testing\"\nfunc TestValue(t *testing.T) { _ = Value(1,2); _ = Other(2,1) }\n")
	writeFixture(t, filepath.Join(dir, "a", "inactive.go"), "//go:build impact_special\n\npackage a\nfunc Inactive(a,b int) int { return a * b }\n")
	writeFixture(t, filepath.Join(dir, "b", "code.go"), "package b\nimport \"example.com/impact/a\"\nfunc Value(x,y int) int { return a.Value(x,y) + 1 }\n")
	writeFixture(t, filepath.Join(dir, "b", "code_test.go"), "package b\nimport \"testing\"\nfunc TestValue(t *testing.T) { _ = Value(1,2) }\n")
	writeFixture(t, filepath.Join(dir, "other", "code.go"), "package other\nfunc Value(a,b int) int { return a + b }\n")
	writeFixture(t, filepath.Join(dir, "other", "code_test.go"), "package other\nimport \"testing\"\nfunc TestValue(t *testing.T) { _ = Value(1,2) }\n")
	impactGit(t, dir, "init")
	impactGit(t, dir, "add", ".")
	impactGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")

	return dir
}

func clearImpactGitEnvironment(t *testing.T) {
	t.Helper()
	// Inherited hook Git state must not redirect these fixture repositories.
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "GIT_") {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestImpactFixtureGitEnvironmentIsolation(t *testing.T) {
	victim := t.TempDir()
	writeFixture(t, filepath.Join(victim, "victim.txt"), "sacrificial repository\n")
	impactGit(t, victim, "init")
	impactGit(t, victim, "add", ".")
	impactGit(t, victim, "-c", "user.name=Victim", "-c", "user.email=victim@example.com", "commit", "-m", "victim baseline")
	snapshot := make(map[string][]byte)
	for _, name := range []string{"config", "HEAD", "index"} {
		contents, err := os.ReadFile(filepath.Join(victim, ".git", name)) // #nosec G304 -- test-owned sacrificial repository.
		if err != nil {
			t.Fatal(err)
		}
		snapshot[name] = contents
	}
	victimHead := impactGit(t, victim, "rev-parse", "HEAD")
	hostile := map[string]string{
		"GIT_DIR":              filepath.Join(victim, ".git"),
		"GIT_COMMON_DIR":       filepath.Join(victim, ".git"),
		"GIT_WORK_TREE":        victim,
		"GIT_INDEX_FILE":       filepath.Join(victim, ".git", "index"),
		"GIT_OBJECT_DIRECTORY": filepath.Join(victim, ".git", "objects"),
		"GIT_CONFIG_COUNT":     "1",
		"GIT_CONFIG_KEY_0":     "core.worktree",
		"GIT_CONFIG_VALUE_0":   victim,
	}
	for name, value := range hostile {
		t.Setenv(name, value)
	}
	// Do not clear the caller environment: each fixture subprocess owns isolation.
	dir := newImpactFixture(t)
	output := impactGit(t, dir, "rev-parse", "--show-toplevel")
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("fixture repository = %q, want %q", got, want)
	}
	if subject := strings.TrimSpace(string(impactGit(t, dir, "log", "-1", "--format=%s"))); subject != "baseline" {
		t.Errorf("fixture commit = %q, want baseline", subject)
	}
	for name, before := range snapshot {
		after, err := os.ReadFile(filepath.Join(victim, ".git", name)) // #nosec G304 -- test-owned sacrificial repository.
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("victim %s changed", name)
		}
	}
	if after := impactGit(t, victim, "rev-parse", "HEAD"); !bytes.Equal(victimHead, after) {
		t.Error("victim resolved HEAD changed")
	}
	for name, value := range hostile {
		if os.Getenv(name) != value {
			t.Errorf("caller %s was cleared instead of isolating subprocess", name)
		}
	}
}

func TestCLIImpactScope(t *testing.T) {
	clearImpactGitEnvironment(t)
	for _, tc := range []struct {
		name, scope, reason, effective string
		edit                           func(*testing.T, string)
		args                           []string
		calling                        string
		wantA, wantB, wantOther        bool
		strictALine                    int
	}{
		{name: "disabled", effective: "disabled", wantA: true, wantB: true, wantOther: true},
		{name: "empty", scope: "changed-line", effective: "empty", reason: "no selected"},
		{name: "test-only", scope: "changed-line", effective: "empty", reason: "--diff-scope changed-function", edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "a", "extra_test.go"), "package a\n// improve tests\n")
		}},
		{name: "strict mixed production and test", scope: "changed-line", effective: "changed-line", reason: "changed-line", wantA: true, strictALine: 4, edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "a", "code.go"), "package a\nfunc Value(a,b int) int {\n _ = a + 0\n return a - b\n}\nfunc Other(a,b int) int { return a - b }\n")
			writeFixture(t, filepath.Join(dir, "a", "extra_test.go"), "package a\n")
		}},
		{name: "strict deletion-only", scope: "changed-line", effective: "empty", reason: "--diff-scope changed-function", edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "a", "code.go"), "package a\nfunc Value(a,b int) int {\n return a + b\n}\nfunc Other(a,b int) int { return a - b }\n")
		}},
		{name: "test-only impact", scope: "changed-function", effective: "affected-package", reason: "test change", edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "a", "extra_test.go"), "package a\n")
		}, wantA: true, wantB: true},
		{name: "deletion-only", scope: "changed-function", effective: "affected-package", reason: "deletion fallback", edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "a", "code.go"), "package a\nfunc Value(a,b int) int {\n return a + b\n}\nfunc Other(a,b int) int { return a - b }\n")
		}, wantA: true, wantB: true},
		{name: "package", scope: "affected-package", effective: "affected-package", reason: "requested package", edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "a", "code.go"), "package a\nfunc Value(a,b int) int {\n _ = a + 0\n return a - b\n}\nfunc Other(a,b int) int { return a - b }\n")
		}, wantA: true, wantB: true},
		{name: "config", scope: "changed-function", effective: "module", reason: "configuration", edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, ".imp.yaml"), "# changed configuration\n")
		}, wantA: true, wantB: true, wantOther: true},
		{name: "calling-directory", scope: "affected-package", effective: "affected-package", calling: "b", reason: "reverse dependency", edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "a", "extra_test.go"), "package a\n")
		}, wantB: true},
		{name: "exclusions", scope: "affected-package", effective: "affected-package", args: []string{"--exclude-files", "^a/"}, edit: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "a", "extra_test.go"), "package a\n")
		}, wantB: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			dir := newImpactFixture(t)
			t.Chdir(dir)
			if tc.edit != nil {
				tc.edit(t, dir)
			}
			args := []string{"run", filepath.Join(dir, tc.calling), "--dry-run", "--json"}
			if tc.scope != "" {
				args = append(args, "--diff", "HEAD", "--diff-scope", tc.scope)
			}
			args = append(args, tc.args...)
			payload, err := executeCLI(context.Background(), t, "test", args...)
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Selection struct {
					Requested, Effective string
					Reasons              []string
				} `json:"selection"`
				Groups map[string]map[string][]json.RawMessage `json:"groups"`
			}
			if err := json.Unmarshal([]byte(payload), &report); err != nil {
				t.Fatalf("%v\n%s", err, payload)
			}
			if report.Selection.Effective != tc.effective {
				t.Errorf("effective=%s want %s\n%s", report.Selection.Effective, tc.effective, payload)
			}
			if tc.scope != "" && report.Selection.Requested != tc.scope {
				t.Errorf("requested=%s", report.Selection.Requested)
			}
			if !strings.Contains(strings.Join(report.Selection.Reasons, ";"), tc.reason) {
				t.Errorf("reasons=%v want %s", report.Selection.Reasons, tc.reason)
			}
			for pkg, want := range map[string]bool{"a": tc.wantA, "b": tc.wantB, "other": tc.wantOther} {
				path := pkg + "/code.go"
				if tc.calling == pkg {
					path = "code.go"
				}
				got := len(report.Groups["RUNNABLE"][path]) > 0
				if got != want {
					t.Errorf("runnable %s=%t want %t\n%s", path, got, want, payload)
				}
			}
			if tc.strictALine != 0 {
				for _, finding := range report.Groups["RUNNABLE"]["a/code.go"] {
					var mutation struct {
						Line int `json:"line"`
					}
					if err := json.Unmarshal(finding, &mutation); err != nil {
						t.Fatal(err)
					}
					if mutation.Line != tc.strictALine {
						t.Errorf("hidden strict-line mutation: %s", finding)
					}
				}
			}
			if tc.scope != "" && len(report.Groups["RUNNABLE"]["a/inactive.go"]) > 0 {
				t.Error("inactive build-tagged source selected")
			}
		})
	}
}

func TestCLIDocsOnlyDoesNotExecuteMutations(t *testing.T) {
	clearImpactGitEnvironment(t)
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	dir := newImpactFixture(t)
	t.Chdir(dir)
	writeFixture(t, filepath.Join(dir, "README.md"), "# documentation only\n")
	payload, err := executeCLI(context.Background(), t, "test", "run", "--diff", "HEAD", "--diff-scope", "changed-line", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Selection struct {
			Requested, Effective string
			Reasons              []string
		} `json:"selection"`
		Total   int `json:"mutants_total"`
		Skipped int `json:"mutants_skipped"`
	}
	if err := json.Unmarshal([]byte(payload), &document); err != nil {
		t.Fatal(err)
	}
	if document.Total == 0 || document.Skipped != document.Total {
		t.Fatalf("docs-only run executed or selected mutants: %s", payload)
	}
	if document.Selection.Requested != "changed-line" || document.Selection.Effective != "empty" || !strings.Contains(strings.Join(document.Selection.Reasons, ";"), "changed-line ignores non-production input: README.md") {
		t.Fatalf("missing bounded-scope explanation: %s", payload)
	}
}
