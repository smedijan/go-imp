package cmd_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
)

func TestBaselineReadsPreviousReport(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "JSON"}[jsonOutput], func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			dir := t.TempDir()
			writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/fixture\n\ngo 1.25.0\n")
			writeFixture(t, filepath.Join(dir, "code.go"), "package fixture\nfunc Value()int{return 1}\n")
			writeFixture(t, filepath.Join(dir, "code_test.go"), `package fixture
import ("os"; "testing")
func TestReport(t *testing.T) {
	got, err := os.ReadFile("results.json")
	if err != nil { t.Fatal(err) }
	if string(got) != "previous successful report\n" { t.Fatalf("baseline saw altered report: %q", got) }
}
`)
			path := filepath.Join(dir, "results.json")
			writeFixture(t, path, "previous successful report\n")
			configuration.Set(configuration.RunOutputKey, path)
			configuration.Set(configuration.RunJSONKey, jsonOutput)
			t.Chdir(dir)
			stdout, err := executeCLI(context.Background(), t, "test", "run")
			if err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // test-owned output file under t.TempDir
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if json.Valid(payload) != jsonOutput {
				t.Errorf("wrong report format: %s", payload)
			}
			if string(payload) == "previous successful report\n" || stdout != "" {
				t.Error("successful report was not replaced exclusively at its file destination")
			}
		})
	}
}

func TestExecutionFailurePreservesExistingReport(t *testing.T) {
	for _, tc := range []struct {
		name         string
		badDiff      bool
		failBaseline bool
		cancel       bool
		badTemp      bool
		failMutation bool
		want         string
	}{
		{name: "invalid diff", badDiff: true, want: "git"},
		{name: "failing baseline", failBaseline: true, want: "intentional baseline failure"},
		{name: "cancelled", cancel: true, want: "context canceled"},
		{name: "temporary directory failure", badTemp: true, want: "workdir"},
		{name: "missing worker source", failMutation: true, want: "apply mutation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			dir := t.TempDir()
			writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/fixture\n\ngo 1.25.0\n")
			writeFixture(t, filepath.Join(dir, "code.go"), "package fixture\nfunc Value()int{return 1+0}\n")
			testBody := "_ = Value()"
			if tc.failBaseline {
				testBody = `t.Fatal("intentional baseline failure")`
			}
			writeFixture(t, filepath.Join(dir, "code_test.go"), "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T){"+testBody+"}\n")
			path := filepath.Join(dir, "results.json")
			writeFixture(t, path, "previous successful report\n")
			configuration.Set(configuration.RunOutputKey, path)
			configuration.Set(configuration.RunJSONKey, true)
			if tc.failMutation {
				configuration.Set(configuration.RunExcludeWorkdir, []string{"code.go"})
			}
			if tc.badDiff {
				configuration.Set(configuration.RunDiffRef, "invalid-ref-for-report-test")
			}
			if tc.badTemp {
				for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
					t.Setenv(key, filepath.Join(dir, "missing"))
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			// Coverage executes in the module root; keep that process-wide change scoped to this case.
			t.Chdir(dir)
			stdout, err := executeCLI(ctx, t, "test", "run")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation not preserved: %v", err)
			}
			//nolint:gosec // path is a test-owned report under t.TempDir
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != "previous successful report\n" {
				t.Errorf("failed execution destroyed the previous report: %q", got)
			}
			if stdout != "" {
				t.Errorf("file output leaked to stdout: %s", stdout)
			}
			leftovers, globErr := filepath.Glob(filepath.Join(dir, ".imp-report-*"))
			if globErr != nil || len(leftovers) != 0 {
				t.Errorf("temporary reports not cleaned up: %v, %v", leftovers, globErr)
			}
		})
	}
}
