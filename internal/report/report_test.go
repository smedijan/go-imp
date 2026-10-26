package report_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/token"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/execution"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
	"github.com/smedijan/go-imp/internal/report/internal"
)

type stubMutant struct {
	mutator.Mutator
	position   token.Position
	status     mutator.Status
	mutantType mutator.Type
	command    []string
	original   []byte
	mutated    []byte
}

func (m stubMutant) Position() token.Position { return m.position }
func (m stubMutant) Status() mutator.Status   { return m.status }
func (m stubMutant) Type() mutator.Type       { return m.mutantType }
func (m stubMutant) TestCommand() []string    { return m.command }
func (m stubMutant) OrigSnippet() []byte      { return m.original }
func (m stubMutant) MutatedSnippet() []byte   { return m.mutated }

func fixtureResults() report.Results {
	var mutants []mutator.Mutator
	for _, status := range []mutator.Status{mutator.Lived, mutator.NotCovered, mutator.TimedOut,
		mutator.Runnable, mutator.Killed, mutator.Skipped, mutator.NotViable} {
		mutants = append(mutants, stubMutant{
			position: token.Position{Filename: "source.go", Line: int(status) + 1, Column: 4},
			status:   status, mutantType: mutator.ConditionalsBoundary,
			command:  []string{"go", "test", "-tags", "tag1 tag2", "example.com/pkg"},
			original: []byte("return a > b\n"), mutated: []byte("return a >= b\n"),
		})
	}

	return report.Results{Module: "example.com", Mutants: mutants, Elapsed: 2345 * time.Millisecond}
}

func renderJSON(t *testing.T, results report.Results, options report.Options) (internal.OutputResult, string) {
	t.Helper()
	options.JSON = true
	var out bytes.Buffer
	if err := report.Write(&out, results, options); err != nil {
		t.Fatal(err)
	}
	var document internal.OutputResult
	decoder := json.NewDecoder(&out)
	payload := out.String()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("expected exactly one JSON document, got %v", err)
	}

	return document, payload
}

func TestParseStatuses(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    []mutator.Status
		invalid bool
	}{
		{value: "lived,not-covered", want: []mutator.Status{mutator.Lived, mutator.NotCovered}},
		{value: "killed,lived,lived", want: []mutator.Status{mutator.Lived, mutator.Killed}},
		{value: " timed-out, runnable ", want: []mutator.Status{mutator.TimedOut, mutator.Runnable}},
		{value: "skipped,not-viable", want: []mutator.Status{mutator.Skipped, mutator.NotViable}},
		{value: "lc", invalid: true}, {value: "covered", invalid: true},
		{value: "lived,", invalid: true}, {value: "typo", invalid: true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := report.ParseStatuses(tc.value)
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v", err)
			}
			if !tc.invalid {
				if diff := cmp.Diff(tc.want, got); diff != "" {
					t.Error(diff)
				}
			}
		})
	}
	all, err := report.ParseStatuses("")
	if err != nil || len(all) != 7 {
		t.Fatalf("default statuses = %v, %v", all, err)
	}
}

func TestJSONReport(t *testing.T) {
	document, payload := renderJSON(t, fixtureResults(), report.Options{Color: true, Verbose: true})
	if document.SchemaVersion != 1 || document.GoModule != "example.com" || math.Abs(document.ElapsedTime-2.345) > 1e-9 {
		t.Fatalf("incorrect metadata: %+v", document)
	}
	if document.MutantsTotal != 7 || len(document.Groups) != 7 || len(document.SelectedStatuses) != 7 {
		t.Fatalf("missing statuses: %+v", document)
	}
	if document.TestEfficacy != 50 || math.Abs(document.MutationsCoverage-200.0/3.0) > 1e-9 {
		t.Errorf("incorrect rates: efficacy %f, coverage %f", document.TestEfficacy, document.MutationsCoverage)
	}
	finding := document.Groups["LIVED"]["source.go"][0]
	if finding.Line != int(mutator.Lived)+1 || finding.Column != 4 || finding.Type != "CONDITIONALS_BOUNDARY" ||
		finding.TestCommand != `go test -tags "tag1 tag2" example.com/pkg` {
		t.Errorf("incorrect finding: %+v", finding)
	}
	for _, forbidden := range []string{`"original"`, `"mutated"`, `"status"`, `"file_name"`, "\x1b[", "Tests passed"} {
		if strings.Contains(payload, forbidden) {
			t.Errorf("unexpected JSON content: %q", forbidden)
		}
	}
}

func TestReportStatusFilter(t *testing.T) {
	statuses, _ := report.ParseStatuses("killed")
	document, _ := renderJSON(t, fixtureResults(), report.Options{Statuses: statuses})
	if len(document.Groups) != 1 || len(document.Groups["KILLED"]["source.go"]) != 1 || document.MutantsTotal != 7 {
		t.Fatalf("incorrect filtered report: %+v", document)
	}
	if document.MutantsLived != 1 || document.TestEfficacy != 50 || document.MutantsTimedOut != 1 {
		t.Error("filter changed aggregate metrics")
	}
	if diff := cmp.Diff([]string{"killed"}, document.SelectedStatuses); diff != "" {
		t.Error(diff)
	}
	var out bytes.Buffer
	if err := report.Write(&out, fixtureResults(), report.Options{Statuses: statuses}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "LIVED (1)") || !strings.Contains(out.String(), "KILLED (1)") ||
		!strings.Contains(out.String(), "Total: 7") {
		t.Fatalf("bad text filtering: %s", &out)
	}
}

func TestEmptyAndNoMatchingJSON(t *testing.T) {
	statuses, _ := report.ParseStatuses("killed")
	for _, results := range []report.Results{{Module: "empty"}, {Mutants: []mutator.Mutator{stubMutant{status: mutator.Lived}}}} {
		document, payload := renderJSON(t, results, report.Options{Statuses: statuses})
		if document.Groups == nil || len(document.Groups) != 0 || !strings.Contains(payload, `"groups":{}`) {
			t.Fatalf("expected empty object: %s", payload)
		}
		if document.MutantsTotal != len(results.Mutants) {
			t.Error("incorrect total")
		}
	}
}

func TestReportStableOrdering(t *testing.T) {
	results := fixtureResults()
	results.Mutants = append(results.Mutants,
		stubMutant{status: mutator.Lived, position: token.Position{Filename: "a.go", Line: 2, Column: 1}},
		stubMutant{status: mutator.Lived, position: token.Position{Filename: "source.go", Line: 1, Column: 1}},
	)
	for _, jsonOutput := range []bool{false, true} {
		var first, second bytes.Buffer
		options := report.Options{JSON: jsonOutput}
		if err := report.Write(&first, results, options); err != nil {
			t.Fatal(err)
		}
		for i, j := 0, len(results.Mutants)-1; i < j; i, j = i+1, j-1 {
			results.Mutants[i], results.Mutants[j] = results.Mutants[j], results.Mutants[i]
		}
		if err := report.Write(&second, results, options); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(first.String(), second.String()); diff != "" {
			t.Error(diff)
		}
	}
}

func TestHumanReport(t *testing.T) {
	var out bytes.Buffer
	if err := report.Write(&out, fixtureResults(), report.Options{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, status := range []string{"LIVED", "NOT COVERED", "TIMED OUT", "RUNNABLE", "KILLED", "SKIPPED", "NOT VIABLE"} {
		if strings.Count(text, status+" (1)") != 1 {
			t.Errorf("missing or repeated group %s", status)
		}
	}
	for _, expected := range []string{"-return a > b", "+return a >= b", "equivalent mutations", "Test efficacy: 50.00%"} {
		if !strings.Contains(text, expected) {
			t.Errorf("missing %q", expected)
		}
	}
	for _, forbidden := range []string{"experimental", "Mutation (- original", "\x1b["} {
		if strings.Contains(text, forbidden) {
			t.Errorf("unexpected %q", forbidden)
		}
	}
	// Only lived and uncovered mutants get diffs without verbose mode.
	if strings.Count(text, "-return a > b") != 2 {
		t.Errorf("unexpected default diff count: %s", text)
	}
	out.Reset()
	if err := report.Write(&out, fixtureResults(), report.Options{Verbose: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "-return a > b") != 7 || strings.Count(out.String(), "Test command:") != 7 {
		t.Errorf("verbose must expand every status: %s", &out)
	}
}

func TestReportDryRun(t *testing.T) {
	results := report.Results{Mutants: []mutator.Mutator{stubMutant{status: mutator.Runnable}, stubMutant{status: mutator.NotCovered}}}
	document, _ := renderJSON(t, results, report.Options{DryRun: true, EfficacyThreshold: 99, CoverageThreshold: 99})
	if !document.DryRun || document.MutationsCoverage != 50 {
		t.Errorf("incorrect dry-run metrics: %+v", document)
	}
}

func TestQualityGatesAfterReport(t *testing.T) {
	for _, tc := range []struct {
		options report.Options
		code    int
	}{
		{options: report.Options{JSON: true, EfficacyThreshold: 80}, code: 10},
		{options: report.Options{JSON: true, CoverageThreshold: 80}, code: 11},
	} {
		var out bytes.Buffer
		// Selecting a status with no matches must not bypass quality gates.
		results := report.Results{Mutants: []mutator.Mutator{stubMutant{status: mutator.Lived}, stubMutant{status: mutator.NotCovered}}}
		tc.options.Statuses = []mutator.Status{mutator.Killed}
		err := report.Write(&out, results, tc.options)
		var gate *execution.ExitError
		if !errors.As(err, &gate) || gate.ExitCode() != tc.code {
			t.Fatalf("expected %d, got %v", tc.code, err)
		}
		if !json.Valid(out.Bytes()) {
			t.Fatalf("gate failure swallowed report: %s", &out)
		}
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReportWriteFailure(t *testing.T) {
	if err := report.Write(failedWriter{}, fixtureResults(), report.Options{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected write error, got %v", err)
	}
}
