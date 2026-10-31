// Package report renders mutation results for humans and automation.
package report

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/smedijan/go-imp/internal/execution"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report/internal"
)

// Results contains unique discovered edits and the elapsed execution time.
// Counts are exclusive: each edit is attributed only to its primary Type(),
// even when multiple enabled families contributed the same edit.
type Results struct {
	Module  string
	Mutants []mutator.Mutator
	Elapsed time.Duration
}

// Options selects presentation and quality gates, never which mutants are executed.
type Options struct {
	JSON              bool
	Verbose           bool
	Color             bool
	DryRun            bool
	Statuses          []mutator.Status
	EfficacyThreshold float64
	CoverageThreshold float64
}

var statusOrder = []mutator.Status{
	mutator.Lived, mutator.NotCovered, mutator.TimedOut, mutator.Runnable,
	mutator.Killed, mutator.Skipped, mutator.NotViable,
}

var statusNames = map[mutator.Status]string{
	mutator.Lived: "lived", mutator.NotCovered: "not-covered", mutator.TimedOut: "timed-out",
	mutator.Runnable: "runnable", mutator.Killed: "killed", mutator.Skipped: "skipped", mutator.NotViable: "not-viable",
}

// ParseStatuses validates a comma-separated list and returns statuses in triage order.
// An empty list selects every status.
func ParseStatuses(value string) ([]mutator.Status, error) {
	selected := make(map[mutator.Status]bool)
	if strings.TrimSpace(value) == "" {
		return append([]mutator.Status(nil), statusOrder...), nil
	}
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		found := false
		for status, valid := range statusNames {
			if name == valid {
				selected[status] = true
				found = true

				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown status %q; use lived, not-covered, timed-out, runnable, killed, skipped, or not-viable", name)
		}
	}
	var statuses []mutator.Status
	for _, status := range statusOrder {
		if selected[status] {
			statuses = append(statuses, status)
		}
	}

	return statuses, nil
}

// Write renders one complete report, then assesses the unfiltered quality gates.
// It neither logs nor opens files; callers own the output destination.
func Write(out io.Writer, results Results, options Options) error {
	if len(options.Statuses) == 0 {
		options.Statuses = append([]mutator.Status(nil), statusOrder...)
	}
	ordered := sortedMutants(results.Mutants)
	document := newDocument(results, ordered, options)
	var buffer bytes.Buffer
	if options.JSON {
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(document); err != nil {
			return fmt.Errorf("encode report: %w", err)
		}
	} else {
		writeText(&buffer, document, ordered, options)
	}
	if _, err := buffer.WriteTo(out); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return assess(document, options)
}

func sortedMutants(mutants []mutator.Mutator) []mutator.Mutator {
	ordered := append([]mutator.Mutator(nil), mutants...)
	slices.SortStableFunc(ordered, func(a, b mutator.Mutator) int {
		pa, pb := a.Position(), b.Position()

		return cmp.Or(
			cmp.Compare(pa.Filename, pb.Filename),
			cmp.Compare(pa.Line, pb.Line),
			cmp.Compare(pa.Column, pb.Column),
			cmp.Compare(a.Type().String(), b.Type().String()),
		)
	})

	return ordered
}

func newDocument(results Results, ordered []mutator.Mutator, options Options) internal.OutputResult {
	document := internal.OutputResult{
		SchemaVersion: 1, GoModule: results.Module, DryRun: options.DryRun,
		ElapsedTime: results.Elapsed.Seconds(), MutantsTotal: len(results.Mutants),
		TestEfficacy: 100, MutationsCoverage: 100,
		SelectedStatuses:  make([]string, 0, len(options.Statuses)),
		Groups:            make(map[string]map[string][]internal.Mutation),
		MutatorStatistics: make(map[string]int),
	}
	for _, status := range options.Statuses {
		document.SelectedStatuses = append(document.SelectedStatuses, statusNames[status])
	}
	for _, m := range ordered {
		countStatus(&document, m.Status())
		document.MutatorStatistics[strings.ToLower(m.Type().String())]++
		if selected(options.Statuses, m.Status()) {
			addFinding(&document, m)
		}
	}
	tested := document.MutantsKilled + document.MutantsLived
	if tested > 0 {
		document.TestEfficacy = float64(document.MutantsKilled) / float64(tested) * 100
	}
	covered := tested
	if options.DryRun {
		covered = document.MutantsRunnable
	}
	if covered+document.MutantsNotCovered > 0 {
		document.MutationsCoverage = float64(covered) / float64(covered+document.MutantsNotCovered) * 100
	}

	return document
}

func countStatus(document *internal.OutputResult, status mutator.Status) {
	switch status {
	case mutator.Lived:
		document.MutantsLived++
	case mutator.NotCovered:
		document.MutantsNotCovered++
	case mutator.TimedOut:
		document.MutantsTimedOut++
	case mutator.Runnable:
		document.MutantsRunnable++
	case mutator.Killed:
		document.MutantsKilled++
	case mutator.Skipped:
		document.MutantsSkipped++
	case mutator.NotViable:
		document.MutantsNotViable++
	}
}

func addFinding(document *internal.OutputResult, m mutator.Mutator) {
	status := m.Status().String()
	files, ok := document.Groups[status]
	if !ok {
		files = make(map[string][]internal.Mutation)
		document.Groups[status] = files
	}
	pos := m.Position()
	files[pos.Filename] = append(files[pos.Filename], internal.Mutation{
		Line: pos.Line, Column: pos.Column, Type: m.Type().String(), TestCommand: formatCommand(testCommand(m)),
	})
}

func selected(statuses []mutator.Status, status mutator.Status) bool {
	for _, s := range statuses {
		if s == status {
			return true
		}
	}

	return false
}

func assess(document internal.OutputResult, options Options) error {
	if options.DryRun || document.MutantsTotal == 0 {
		return nil
	}
	if options.EfficacyThreshold > 0 && document.TestEfficacy <= options.EfficacyThreshold {
		return execution.NewExitErr(execution.EfficacyThreshold)
	}
	if options.CoverageThreshold > 0 && document.MutationsCoverage <= options.CoverageThreshold {
		return execution.NewExitErr(execution.MutantCoverageThreshold)
	}

	return nil
}
