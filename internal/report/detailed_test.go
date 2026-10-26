package report_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/log"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
	"github.com/smedijan/go-imp/internal/report/internal"
)

type detailedMutant struct {
	stubMutant
	command []string
}

func (m detailedMutant) TestCommand() []string { return m.command }

func TestDetailedReport(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	configuration.Set(configuration.ExperimentalKey, []string{configuration.ExperimentalDetailedReport})
	output := filepath.Join(t.TempDir(), "results.json")
	configuration.Set(configuration.RunOutputKey, output)
	out := &bytes.Buffer{}
	log.Init(out, &bytes.Buffer{})
	t.Cleanup(log.Reset)

	mutants := []mutator.Mutator{
		stubMutant{status: mutator.NotCovered, position: newPosition("uncovered.go", 2, 5), mutantType: mutator.ArithmeticBase,
			originSnippet: []byte("return a + b\n"), mutatedSnippet: []byte("return a - b\n")},
		detailedMutant{stubMutant: stubMutant{status: mutator.Lived, position: newPosition("z.go", 9, 10),
			originSnippet: []byte("return a > b\n"), mutatedSnippet: []byte("return a >= b\n"), mutantType: mutator.ConditionalsBoundary},
			command: []string{"go", "test", "-tags", "tag1 tag2", "example.com/pkg"}},
		stubMutant{status: mutator.Lived, position: newPosition("a.go", 3, 4)},
		stubMutant{status: mutator.TimedOut, position: newPosition("timeout.go", 1, 1)},
		stubMutant{status: mutator.Killed, position: newPosition("killed.go", 1, 1)},
		stubMutant{status: mutator.Skipped, position: newPosition("skipped.go", 1, 1)},
		stubMutant{status: mutator.NotViable, position: newPosition("invalid.go", 1, 1)},
		stubMutant{status: mutator.Runnable, position: newPosition("pending.go", 1, 1)},
		stubMutant{status: mutator.Lived, position: newPosition("a.go", 7, 2)},
	}
	data := report.Results{Mutants: mutants}
	if err := report.Do(data); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Detailed report (experimental)", "Total: 9 | Survived (LIVED): 3 | Not covered: 1 | Timed out: 1",
		"Killed: 1 | Skipped: 1 | Not viable: 1 | Runnable (not tested): 1",
		"Test efficacy: 25.00%", "Mutator coverage: 80.00%",
		"LIVED (3)", "NOT COVERED (1)", "TIMED OUT (1)", "RUNNABLE (1)",
		"10:9 CONDITIONALS_BOUNDARY", "-return a > b", "+return a >= b",
		"-return a + b", "+return a - b", "not necessarily a fix", "equivalent mutations",
		"Test command: go test -tags \"tag1 tag2\" example.com/pkg",
		"Test execution context unavailable.", "Source context unavailable.",
		"NOT COVERED (1) - Not exercised by coverage; mutations were not tested.",
		"TIMED OUT (1) - Test execution timed out; not classified as survivors.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in report:\n%s", want, got)
		}
	}
	for _, hidden := range []string{"killed.go", "skipped.go", "invalid.go"} {
		if strings.Contains(got, hidden) {
			t.Errorf("lower-priority finding %s should be collapsed", hidden)
		}
	}
	if strings.Index(got, "  a.go") > strings.Index(got, "  z.go") ||
		strings.Index(got, "2:7 ARITHMETIC_BASE") > strings.Index(got, "4:3 ARITHMETIC_BASE") ||
		strings.Index(got, "LIVED (3)") > strings.Index(got, "NOT COVERED (1)") {
		t.Error("findings not sorted by priority, file and position")
	}
	// Execution completion order must not affect the report.
	for i, j := 0, len(mutants)-1; i < j; i, j = i+1, j-1 {
		mutants[i], mutants[j] = mutants[j], mutants[i]
	}
	out.Reset()
	if err := report.Do(data); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(got, out.String()); diff != "" {
		t.Errorf("unstable report (-want +got):\n%s", diff)
	}

	//nolint:gosec // output is a test-owned file in t.TempDir
	payload, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	checkGroupedJSON(t, payload, len(mutants))
	out.Reset()
	for i, j := 0, len(mutants)-1; i < j; i, j = i+1, j-1 {
		mutants[i], mutants[j] = mutants[j], mutants[i]
	}
	if err := report.Do(data); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // output is a test-owned file in t.TempDir
	reordered, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(payload), string(reordered)); diff != "" {
		t.Errorf("unstable grouped JSON (-want +got):\n%s", diff)
	}
}

func checkGroupedJSON(t *testing.T, payload []byte, total int) {
	t.Helper()
	for _, field := range []string{"original", "mutated", "status", "file_name", "files"} {
		if strings.Contains(string(payload), `"`+field+`"`) {
			t.Errorf("redundant field %s should not appear in grouped JSON", field)
		}
	}
	if strings.Count(string(payload), `"test_command"`) != 1 {
		t.Error("test_command should be omitted when unavailable")
	}
	var structured internal.DetailedOutputResult
	if err := json.Unmarshal(payload, &structured); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, files := range structured.Groups {
		for _, findings := range files {
			count += len(findings)
		}
	}
	if count != total || structured.MutantsTotal != total {
		t.Errorf("JSON has %d mutants, total %d, want %d", count, structured.MutantsTotal, total)
	}
	for _, status := range []string{"LIVED", "NOT COVERED", "TIMED OUT", "KILLED", "SKIPPED", "NOT VIABLE", "RUNNABLE"} {
		if len(structured.Groups[status]) == 0 {
			t.Errorf("missing status group %s", status)
		}
	}
	if structured.MutantsTimedOut != 1 || structured.MutantsSkipped != 1 || structured.MutantsRunnable != 1 {
		t.Errorf("incorrect grouped summary: %+v", structured.OutputSummary)
	}
	want := []internal.DetailedMutation{{Line: 10, Column: 9, Type: "CONDITIONALS_BOUNDARY",
		TestCommand: `go test -tags "tag1 tag2" example.com/pkg`}}
	if diff := cmp.Diff(want, structured.Groups["LIVED"]["z.go"]); diff != "" {
		t.Errorf("grouped mutation mismatch (-want +got):\n%s", diff)
	}
	wantSorted := []internal.DetailedMutation{
		{Line: 2, Column: 7, Type: "ARITHMETIC_BASE"},
		{Line: 4, Column: 3, Type: "ARITHMETIC_BASE"},
	}
	if diff := cmp.Diff(wantSorted, structured.Groups["LIVED"]["a.go"]); diff != "" {
		t.Errorf("grouped findings are not sorted (-want +got):\n%s", diff)
	}
}

func TestDetailedReportExplainsStatusesOnce(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	configuration.Set(configuration.ExperimentalKey, []string{configuration.ExperimentalDetailedReport})
	out := &bytes.Buffer{}
	log.Init(out, &bytes.Buffer{})
	t.Cleanup(log.Reset)

	var mutants []mutator.Mutator
	for _, status := range []mutator.Status{mutator.Lived, mutator.NotCovered, mutator.TimedOut, mutator.Runnable} {
		for range 2 {
			mutants = append(mutants, stubMutant{status: status, position: fakePosition,
				originSnippet: []byte("a+b\n"), mutatedSnippet: []byte("a-b\n")})
		}
	}
	if err := report.Do(report.Results{Mutants: mutants}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, title := range []string{
		"LIVED (2) - Tests passed; review assertions and edge cases.",
		"NOT COVERED (2) - Not exercised by coverage; mutations were not tested.",
		"TIMED OUT (2) - Test execution timed out; not classified as survivors.",
		"RUNNABLE (2) - Covered, but mutations were not tested.",
	} {
		if strings.Count(got, title) != 1 {
			t.Errorf("expected one group title %q in report:\n%s", title, got)
		}
	}
	for _, repeated := range []string{
		"      Tests passed", "      Not exercised", "      Test execution timed out",
		"      Covered, but", "Mutation (- original, + mutated):",
	} {
		if strings.Contains(got, repeated) {
			t.Errorf("unexpected per-finding explanation %q", repeated)
		}
	}
	if strings.Count(got, "-a+b") != len(mutants) || strings.Count(got, "+a-b") != len(mutants) {
		t.Error("missing mutation diffs")
	}
}

func TestDetailedReportPreservesDefaultJSON(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	out := &bytes.Buffer{}
	log.Init(out, &bytes.Buffer{})
	t.Cleanup(log.Reset)
	output := filepath.Join(t.TempDir(), "results.json")
	configuration.Set(configuration.RunOutputKey, output)
	data := report.Results{Mutants: []mutator.Mutator{
		detailedMutant{stubMutant: stubMutant{status: mutator.Lived, position: fakePosition,
			originSnippet: []byte("a+b"), mutatedSnippet: []byte("a-b")}, command: []string{"go", "test", "./..."}},
	}}
	if err := report.Do(data); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // output is a test-owned file in t.TempDir
	payload, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"original", "mutated", "test_command", "groups"} {
		if strings.Contains(string(payload), `"`+field+`"`) {
			t.Errorf("experimental field %s appeared without opt-in", field)
		}
	}
	if strings.Contains(out.String(), "Detailed report") {
		t.Fatal("detailed report appeared without opt-in")
	}
}

func TestDetailedReportDryRunAndEmpty(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	configuration.Set(configuration.ExperimentalKey, []string{configuration.ExperimentalDetailedReport})
	configuration.Set(configuration.RunDryRunKey, true)
	out := &bytes.Buffer{}
	log.Init(out, &bytes.Buffer{})
	t.Cleanup(log.Reset)
	if err := report.Do(report.Results{Mutants: []mutator.Mutator{
		stubMutant{status: mutator.Runnable, position: fakePosition},
		stubMutant{status: mutator.NotCovered, position: fakePosition},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Runnable (not tested): 1") || !strings.Contains(got, "Mutator coverage: 50.00%") {
		t.Errorf("incorrect dry-run report: %s", got)
	}
	out.Reset()
	if err := report.Do(report.Results{}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "\nNo results to report.\n" {
		t.Errorf("empty results: %q", got)
	}
}

func TestDetailedReportLogger(t *testing.T) {
	for _, tc := range []struct {
		name       string
		filter     string
		diffFilter string
		visible    bool
	}{
		{name: "collapsed"},
		{name: "explicit status output", filter: "k", visible: true},
		{name: "explicit diff output", diffFilter: "k", visible: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			configuration.Set(configuration.ExperimentalKey, []string{configuration.ExperimentalDetailedReport})
			configuration.Set(configuration.RunOutputStatusesKey, tc.filter)
			configuration.Set(configuration.RunOutputDiffStatusesKey, tc.diffFilter)
			out := &bytes.Buffer{}
			log.Init(out, &bytes.Buffer{})
			t.Cleanup(log.Reset)
			report.NewLogger().Mutant(stubMutant{status: mutator.Killed, position: fakePosition})
			if got := strings.Contains(out.String(), "KILLED"); got != tc.visible {
				t.Errorf("per-mutant output visible = %v, want %v", got, tc.visible)
			}
		})
	}
}
