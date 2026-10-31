package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/execution"
)

func TestReportingConfigurationPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    map[string]string
		args   []string
		json   bool
		status string
	}{
		{name: "file", json: true, status: "lived"},
		{name: "environment statuses", env: map[string]string{"IMP_RUN_STATUSES": "killed"}, json: true, status: "killed"},
		{name: "environment format", env: map[string]string{"IMP_RUN_JSON": "false"}},
		{name: "flags", env: map[string]string{"IMP_RUN_JSON": "false", "IMP_RUN_STATUSES": "killed"}, args: []string{"--json", "--statuses", "timed-out"}, json: true, status: "timed-out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			dir := newModuleFixture(t)
			writeFixture(t, filepath.Join(dir, ".imp.yaml"), "quiet: true\nno-color: true\nrun:\n  json: true\n  verbose: true\n  statuses: lived\n  threshold:\n    efficacy: 80\n")
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			t.Chdir(dir)
			args := append([]string{"run", "--dry-run"}, tc.args...)
			out, err := executeCLI(context.Background(), t, "test", args...)
			if err != nil {
				t.Fatal(err)
			}
			checkCLIPayload(t, []byte(out), tc.json, false)
			if tc.json {
				var document struct {
					Statuses []string `json:"selected_statuses"`
				}
				if err := json.Unmarshal([]byte(out), &document); err != nil {
					t.Fatal(err)
				}
				if len(document.Statuses) != 1 || document.Statuses[0] != tc.status {
					t.Fatalf("statuses=%v, want %s", document.Statuses, tc.status)
				}
			}
		})
	}
}

func TestInvalidStatusIsUsageError(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	_, err := executeCLI(context.Background(), t, "test", "run", "--statuses", "typo")
	var usage *execution.UsageError
	if !errors.As(err, &usage) || usage.ExitCode() != 2 {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestReportingHelp(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	text, err := executeCLI(context.Background(), t, "test", "run", "--statuses", "invalid", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Examples:", "Reporting:", "Scope:", "Execution:", "Quality gates:", "Mutation operators:", "--json", "--statuses", "--quiet", "--no-color", "--boolean-return", "--error-return"} {
		if !strings.Contains(text, expected) {
			t.Errorf("help missing %q:\n%s", expected, text)
		}
	}
	for _, removed := range []string{"--experimental", "--output-statuses", "--output-diff-statuses", "--silent"} {
		if strings.Contains(text, removed) {
			t.Errorf("obsolete flag %s in help", removed)
		}
	}
	if strings.Index(text, "Examples:") > strings.Index(text, "Reporting:") {
		t.Error("examples must precede flags")
	}
}

func TestCLIOutputContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "imp.exe")
	//nolint:gosec // build a test-owned binary at a path under t.TempDir
	build := exec.Command("go", "build", "-o", binary, "./imp")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/triage\n\ngo 1.25.0\n")
	writeFixture(t, filepath.Join(dir, "code.go"), "package triage\nfunc Boundary(n int)bool{return n>0}\nfunc Unused(a,b int)int{return a+b}\n")
	writeFixture(t, filepath.Join(dir, "code_test.go"), "package triage\nimport \"testing\"\nfunc TestBoundary(t *testing.T){if !Boundary(2){t.Fatal(\"positive\")}}\n")
	config := filepath.Join(dir, ".imp.yaml")
	writeFixture(t, config, "{}\n")
	for _, tc := range []struct {
		name         string
		args         []string
		file         string
		json         bool
		exit         int
		noReport     bool
		config       string
		failBaseline bool
	}{
		{name: "human stdout", args: []string{"--dry-run"}},
		{name: "JSON stdout", args: []string{"--dry-run", "--json", "--statuses", "runnable"}, json: true},
		{name: "quiet JSON stdout", args: []string{"--dry-run", "--json", "--quiet"}, json: true},
		{name: "config selects JSON", args: []string{"--dry-run"}, config: "run:\n  json: true\n  statuses: runnable\n", json: true},
		{name: "flag overrides config format", args: []string{"--dry-run", "--json=false"}, config: "run:\n  json: true\n"},
		{name: "explicit stdout", args: []string{"--dry-run", "--json", "--output", "-"}, json: true},
		{name: "text ignores extension", args: []string{"--dry-run", "--output", "text.json"}, file: "text.json"},
		{name: "JSON file", args: []string{"--json", "--statuses", "lived", "--output", "survivors.json"}, file: "survivors.json", json: true},
		{name: "empty filter", args: []string{"--dry-run", "--json", "--statuses", "killed"}, json: true},
		{name: "quality gate", args: []string{"--json", "--threshold-efficacy", "90"}, json: true, exit: 10},
		{name: "quality gate file", args: []string{"--json", "--threshold-efficacy", "90", "--output", "gate-failed.json"}, file: "gate-failed.json", json: true, exit: 10},
		{name: "invalid status", args: []string{"--statuses", "typo"}, exit: 2, noReport: true},
		{name: "malformed config", config: "run: [broken", exit: 2, noReport: true},
		{name: "unknown flag", args: []string{"--experimental", "detailed-report"}, exit: 2, noReport: true},
		{name: "unwritable destination", args: []string{"--output", filepath.Join(dir, "missing", "report.txt")}, exit: 1, noReport: true},
		{name: "baseline failure", args: []string{"--json", "--quiet"}, exit: 1, noReport: true, failBaseline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := tc.config
			if content == "" {
				content = "{}\n"
			}
			writeFixture(t, config, content)
			if tc.failBaseline {
				writeFixture(t, filepath.Join(dir, "code_test.go"), "package triage\nimport \"testing\"\nfunc TestFailure(t *testing.T){t.Fatal(\"intentional baseline failure\")}\n")
			}
			args := append([]string{"run", "--config", config, "--workers", "2"}, tc.args...)
			//nolint:gosec // binary and arguments are controlled by this test
			command := exec.Command(binary, args...)
			command.Dir = dir
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tc.exit {
				t.Fatalf("exit %d, want %d; stderr: %s", code, tc.exit, &stderr)
			}
			payload := stdout.Bytes()
			if tc.file != "" {
				if stdout.Len() != 0 {
					t.Errorf("file output leaked to stdout: %s", &stdout)
				}
				//nolint:gosec // test-owned output file under t.TempDir
				payload, err = os.ReadFile(filepath.Join(dir, tc.file))
				if err != nil {
					t.Fatal(err)
				}
			}
			checkCLIPayload(t, payload, tc.json, tc.noReport)
			if tc.exit == 0 {
				for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
					if line != "" && !strings.HasPrefix(line, "Progress: ") && !strings.HasPrefix(line, "Discovery: ") && !strings.HasPrefix(line, "Discovery filtered ") {
						t.Errorf("unexpected non-TTY diagnostic: %s", line)
					}
				}
			}
			if tc.exit != 0 && stderr.Len() == 0 {
				t.Error("failure must explain itself on stderr")
			}
			if tc.failBaseline && !strings.Contains(stderr.String(), "intentional baseline failure") {
				t.Errorf("quiet mode swallowed test failure context: %s", &stderr)
			}
		})
	}
}

func checkCLIPayload(t *testing.T, payload []byte, jsonOutput, noReport bool) {
	t.Helper()
	if noReport {
		if len(payload) != 0 {
			t.Errorf("failure leaked stdout: %s", payload)
		}

		return
	}
	if jsonOutput {
		if !json.Valid(payload) {
			t.Fatalf("invalid JSON: %s", payload)
		}
		var doc struct {
			Schema   int                        `json:"schema_version"`
			Statuses []string                   `json:"selected_statuses"`
			Groups   map[string]json.RawMessage `json:"groups"`
		}
		if err := json.Unmarshal(payload, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Schema != 1 || doc.Groups == nil {
			t.Errorf("missing schema/groups: %s", payload)
		}
		if len(doc.Statuses) == 1 && doc.Statuses[0] == "runnable" && len(doc.Groups) != 1 {
			t.Error("filter not applied to JSON")
		}
		if len(doc.Statuses) == 1 && doc.Statuses[0] == "killed" && len(doc.Groups) != 0 {
			t.Error("expected no matching findings in dry run")
		}
	} else if !strings.Contains(string(payload), "Dry run completed") {
		t.Errorf("not a human report: %s", payload)
	}
	if bytes.Contains(payload, []byte("\x1b[")) {
		t.Error("redirected report contains ANSI escapes")
	}
}
