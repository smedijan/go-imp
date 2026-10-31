package cmd_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/triage"
)

func TestTriageCLIAtomicPersistence(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed baseline"}[fail], func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			dir := t.TempDir()
			t.Chdir(dir)
			writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/fixture\n\ngo 1.25.0\n")
			writeFixture(t, filepath.Join(dir, "code.go"), "package fixture\nfunc Value()int{return 1}\n")
			body := "_ = Value()"
			if fail {
				body = `t.Fatal("baseline failure")`
			}
			writeFixture(t, filepath.Join(dir, "code_test.go"), "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T){"+body+"}\n")
			input := filepath.Join(dir, "input.json")
			destination := filepath.Join(dir, "triage.json")
			inputData := `{"version":1,"module":"example.com/fixture","entries":[]}`
			writeFixture(t, input, inputData)
			writeFixture(t, destination, "previous ledger")
			configuration.Set(configuration.RunTriageInputKey, input)
			configuration.Set(configuration.RunTriageOutputKey, destination)
			configuration.Set(configuration.RunDryRunKey, true)
			_, err := executeCLI(context.Background(), t, "test", "run")
			if (err != nil) != fail {
				t.Fatalf("error=%v", err)
			}
			// #nosec G304 -- test-owned ledger path.
			data, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if string(data) != "previous ledger" {
					t.Fatal("failed run replaced ledger")
				}
			} else {
				var got triage.Ledger
				if err := json.Unmarshal(data, &got); err != nil || got.Version != 1 {
					t.Fatalf("bad saved ledger: %s", data)
				}
			}
			// #nosec G304 -- test-owned input path.
			unchanged, err := os.ReadFile(input)
			if err != nil || string(unchanged) != inputData {
				t.Fatal("input modified")
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, ".imp-report-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("staging leaked: %v", leftovers)
			}
		})
	}
}

func TestTriageCLIAliasesFailBeforeExecution(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/fixture\n\ngo 1.25.0\n")
	input := filepath.Join(dir, "triage.json")
	writeFixture(t, input, `{"version":1,"module":"example.com/fixture","entries":[]}`)
	for _, key := range []string{configuration.RunOutputKey, configuration.RunTriageOutputKey} {
		configuration.Set(configuration.RunTriageInputKey, input)
		configuration.Set(key, input)
		_, err := executeCLI(context.Background(), t, "test", "run")
		if err == nil || !strings.Contains(err.Error(), "aliases") {
			t.Fatalf("alias accepted: %v", err)
		}
		configuration.Set(key, "")
	}
	configuration.Set(configuration.RunTriageInputKey, "")
	configuration.Set(configuration.RunHideReviewedKey, true)
	_, err := executeCLI(context.Background(), t, "test", "run")
	if err == nil || !strings.Contains(err.Error(), "requires triage-input") {
		t.Fatalf("filter accepted without input: %v", err)
	}
}

func TestTriageCLILiteralDashInputAliasesFailBeforeExecution(t *testing.T) {
	for _, tc := range []struct{ name, triageInput, triageOutput, reportOutput, replayInput string }{
		{name: "triage input and report output", triageInput: "-", reportOutput: "./-"},
		{name: "triage input and triage output", triageInput: "-", triageOutput: "./-"},
		{name: "replay input and report output", triageInput: "triage.json", replayInput: "-", reportOutput: "./-"},
		{name: "replay input and triage output", triageInput: "triage.json", replayInput: "-", triageOutput: "./-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			dir := t.TempDir()
			t.Chdir(dir)
			writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/fixture\n\ngo 1.25.0\n")
			original := `{"version":1,"module":"example.com/fixture","entries":[]}`
			writeFixture(t, filepath.Join(dir, "-"), original)
			writeFixture(t, filepath.Join(dir, "triage.json"), original)
			configuration.Set(configuration.RunTriageInputKey, tc.triageInput)
			configuration.Set(configuration.RunTriageOutputKey, tc.triageOutput)
			configuration.Set(configuration.RunOutputKey, tc.reportOutput)
			configuration.Set(configuration.RunReplayReportKey, tc.replayInput)
			_, err := executeCLI(context.Background(), t, "test", "run")
			if err == nil || !strings.Contains(err.Error(), "aliases") {
				t.Fatalf("literal dash alias accepted: %v", err)
			}
			data, err := os.ReadFile("-")
			if err != nil || string(data) != original {
				t.Fatalf("literal dash input changed: %s, %v", data, err)
			}
		})
	}
}

func TestTriageCLILiteralDashInputWithReportStdout(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/fixture\n\ngo 1.25.0\n")
	writeFixture(t, filepath.Join(dir, "code.go"), "package fixture\nfunc Value()int{return 1}\n")
	writeFixture(t, filepath.Join(dir, "code_test.go"), "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T){_ = Value()}\n")
	original := `{"version":1,"module":"example.com/fixture","entries":[]}`
	writeFixture(t, filepath.Join(dir, "-"), original)
	configuration.Set(configuration.RunTriageInputKey, "-")
	configuration.Set(configuration.RunOutputKey, "-")
	configuration.Set(configuration.RunDryRunKey, true)
	configuration.Set(configuration.RunJSONKey, true)
	stdout, err := executeCLI(context.Background(), t, "test", "run")
	if err != nil || !json.Valid([]byte(stdout)) {
		t.Fatalf("report stdout rejected: %s, %v", stdout, err)
	}
	data, err := os.ReadFile("-")
	if err != nil || string(data) != original {
		t.Fatal("report stdout overwrote input")
	}
}
