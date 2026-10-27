package diff_test

import (
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/gomodule"
)

func TestDiffPathsAcrossModuleAndCallingDirectories(t *testing.T) {
	for _, tc := range []struct {
		name              string
		invokeFrom        string
		modulePath        string
		changedFileName   string
		unchangedFileName string
	}{
		{name: "nested module", modulePath: "nested", changedFileName: "pkg/changed.go", unchangedFileName: "pkg/unchanged.go"},
		{name: "relative path from subdirectory", invokeFrom: "pkg", modulePath: "../pkg", changedFileName: "changed.go", unchangedFileName: "unchanged.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withoutGitEnvironment(t)
			configuration.Reset()
			t.Cleanup(configuration.Reset)

			repo := t.TempDir()
			moduleRoot := filepath.Join(repo, "nested")
			pkgDir := filepath.Join(moduleRoot, "pkg")
			if err := os.MkdirAll(pkgDir, 0o750); err != nil {
				t.Fatal(err)
			}
			writeFile := func(path, content string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeFile(filepath.Join(moduleRoot, "go.mod"), "module example.com/nested\n\ngo 1.21\n")
			writeFile(filepath.Join(pkgDir, "changed.go"), "package pkg\n\nfunc Changed() int { return 1 }\n")
			writeFile(filepath.Join(pkgDir, "unchanged.go"), "package pkg\n\nfunc Unchanged() int { return 2 }\n")
			runGit(t, repo, "init")
			runGit(t, repo, "config", "user.email", "test@example.com")
			runGit(t, repo, "config", "user.name", "Test")
			runGit(t, repo, "add", ".")
			runGit(t, repo, "commit", "-m", "baseline")
			runGit(t, repo, "commit", "--allow-empty", "-m", "diff base")
			writeFile(filepath.Join(pkgDir, "changed.go"), "package pkg\n\nfunc Changed() int { return 3 }\n")

			oldWD, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			invokeDir := repo
			if tc.invokeFrom != "" {
				invokeDir = pkgDir
			}
			if err := os.Chdir(invokeDir); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(oldWD); err != nil {
					t.Errorf("restore working directory: %v", err)
				}
			})
			mod, err := gomodule.Init(tc.modulePath)
			if err != nil {
				t.Fatal(err)
			}
			configuration.Set(configuration.RunDiffRef, "HEAD~")
			got, err := diff.New(mod)
			if err != nil {
				t.Fatal(err)
			}
			if !got.IsChanged(token.Position{Filename: tc.changedFileName, Line: 3}) {
				t.Fatal("changed source path was not recognized")
			}
			if got.IsChanged(token.Position{Filename: tc.unchangedFileName, Line: 3}) {
				t.Fatal("unchanged source path was recognized as changed")
			}
		})
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	// The arguments are fixed by the test call sites and only invoke Git in a temporary repository.
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null"}, args...)...) // #nosec G204
	cmd.Env = gitEnvironment()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func withoutGitEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "GIT_") {
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Setenv(name, value); err != nil {
					t.Errorf("restore %s: %v", name, err)
				}
			})
		}
	}
}

func gitEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}

	return env
}
