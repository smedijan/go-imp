package report_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

func TestCommandArgumentsRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		argument string
		quoted   string
	}{
		{name: "ordinary", argument: "integration", quoted: "integration"},
		{name: "space", argument: "tag1 tag2", quoted: "'tag1 tag2'"},
		{name: "empty", argument: "", quoted: "''"},
		{name: "variable", argument: "$IMP_QUOTE_TEST", quoted: "'$IMP_QUOTE_TEST'"},
		{name: "substitution", argument: "$(printf substituted)", quoted: "'$(printf substituted)'"},
		{name: "backticks", argument: "`printf substituted`", quoted: "'`printf substituted`'"},
		{name: "single quote", argument: "a'b", quoted: `'a'"'"'b'`},
		{name: "double quote", argument: `a"b`, quoted: `'a"b'`},
		{name: "backslash", argument: `a\b`, quoted: `'a\b'`},
		{name: "semicolon", argument: "a;b", quoted: "'a;b'"},
		{name: "glob", argument: "*.go", quoted: "'*.go'"},
		{name: "newline", argument: "a\nb", quoted: "'a\nb'"},
		{name: "Unicode", argument: "café", quoted: "'café'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("IMP_QUOTE_TEST", "expanded")
			args := []string{"go", "test", "-tags", tc.argument}
			doc, _ := renderJSON(t, report.Results{Mutants: []mutator.Mutator{stubMutant{status: mutator.Lived, command: args}}}, report.Options{})
			command := doc.Groups["LIVED"][""][0].TestCommand
			if want := "go test -tags " + tc.quoted; command != want {
				t.Errorf("command=%q, want %q", command, want)
			}
			if _, err := exec.LookPath("sh"); err != nil {
				t.Skip("POSIX shell unavailable; explicit quoting assertions still ran")
			}
			//nolint:gosec // exercise serialization with fixed, test-owned shell arguments
			output, err := exec.Command("sh", "-c", "printf '%s\\0' "+command).Output()
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(string(bytes.TrimSuffix(output, []byte{0})), "\x00")
			if diff := cmp.Diff(args, got); diff != "" {
				t.Errorf("shell argument round-trip (-want +got):\n%s", diff)
			}
		})
	}
}

type evidenceMutant struct {
	stubMutant
	mutator.EvidenceRecorder
	mutator.ObservationRecorder
}

func TestReportExecutionEvidence(t *testing.T) {
	m := &evidenceMutant{stubMutant: stubMutant{status: mutator.Killed}}
	evidence := &mutator.ExecutionEvidence{Args: []string{"go", "test", "./..."}, PackageScope: "./...", Category: "test", FailingTests: []mutator.FailingTest{{Package: "p", Test: "TestParent/child"}}}
	m.SetExecutionEvidence(evidence)
	doc, payload := renderJSON(t, report.Results{Mutants: []mutator.Mutator{m}}, report.Options{})
	if delta := cmp.Diff(evidence, doc.Groups["KILLED"][""][0].Execution); delta != "" {
		t.Fatal(delta)
	}
	if !strings.Contains(payload, `"schema_version":2`) {
		t.Fatal(payload)
	}
	doc, payload = renderJSON(t, report.Results{Mutants: []mutator.Mutator{stubMutant{status: mutator.NotCovered}}}, report.Options{})
	if doc.Groups["NOT COVERED"][""][0].Execution != nil || strings.Contains(payload, `"execution"`) {
		t.Fatal("unavailable evidence must be omitted")
	}
}

func TestReportFailureEnvelope(t *testing.T) {
	var output bytes.Buffer
	if err := report.WriteFailure(&output, "execution", true); err != nil {
		t.Fatal(err)
	}
	var failure report.Failure
	if err := json.Unmarshal(output.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.SchemaVersion != 2 || failure.Kind != "run_error" || failure.ReportState != "not-committed" || failure.PartialResultsAvailable || !failure.Cancelled {
		t.Fatalf("incorrect envelope: %+v", failure)
	}
}

func TestReportObservationContextAndUnknown(t *testing.T) {
	m := &evidenceMutant{stubMutant: stubMutant{status: mutator.Runnable}}
	observation := &mutator.ObservationContext{Package: mutator.ObservedName{Known: true, Value: "calc"}, EnclosingSymbol: mutator.ObservedName{Known: true, Value: "func::Add", Truncated: true}, Selection: mutator.SelectionObservation{Coverage: "covered", EligibilityReason: "dry-run", DryRun: true, ImpactProvenance: "unknown"}}
	m.SetObservationContext(observation)
	document, _ := renderJSON(t, report.Results{Mutants: []mutator.Mutator{m}}, report.Options{DryRun: true})
	if delta := cmp.Diff(observation, document.Groups["RUNNABLE"][""][0].ObservationContext); delta != "" {
		t.Fatal(delta)
	}
	document, payload := renderJSON(t, report.Results{Mutants: []mutator.Mutator{stubMutant{status: mutator.NotCovered}}}, report.Options{})
	if document.Groups["NOT COVERED"][""][0].ObservationContext != nil || !strings.Contains(payload, `"observation_context":null`) {
		t.Fatalf("unknown context invented from terminal status: %s", payload)
	}
}
