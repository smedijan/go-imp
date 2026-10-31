package report_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
	"github.com/smedijan/go-imp/internal/report/internal"
	"github.com/smedijan/go-imp/internal/triage"
)

type triageMutant struct {
	stubMutant
	identity mutator.Identity
}

func (m triageMutant) Identity() mutator.Identity { return m.identity }
func (m triageMutant) ObservationContext() *mutator.ObservationContext {
	return &mutator.ObservationContext{PackagePath: mutator.ObservedName{Value: "example.test", Known: true}, EnclosingSymbol: mutator.ObservedName{Value: "func:Sub", Known: true}}
}

func triageResults() (report.Results, triage.Ledger) {
	results := report.Results{Module: "example.test"}
	ledger := triage.Ledger{Version: 1, Module: results.Module, Entries: []triage.Entry{}}
	for i := 0; i < 23; i++ {
		base := stubMutant{status: mutator.Lived, mutantType: mutator.ConditionalsBoundary}
		base.position.Filename = "source.go"
		base.position.Column = 4
		base.position.Line = i + 1
		id := mutator.Identity{ID: fmt.Sprintf("imp-v1:%s:%064x", strings.Repeat("a", 64), i), Version: 1, ModulePath: "source.go", Original: ">", Replacement: ">="}
		results.Mutants = append(results.Mutants, triageMutant{stubMutant: base, identity: id})
		if i == 0 {
			ledger.Entries = append(ledger.Entries, triage.Entry{Identity: id, Disposition: "suspected-equivalence", Reason: "manual review hypothesis only", Provenance: "reviewer; ticket #1"})
		}
	}

	return results, ledger
}

func TestTriageGroupingRetainsMutantsAndStableBoundedTasks(t *testing.T) {
	results, _ := triageResults()
	doc, _ := renderJSON(t, results, report.Options{GroupReview: true})
	if len(doc.ReviewGroups) != 3 || len(doc.Groups[mutator.Lived.String()]["source.go"]) != 23 {
		t.Fatalf("lost findings: %+v", doc)
	}
	seen := make(map[string]bool)
	for _, g := range doc.ReviewGroups {
		if len(g.MutantIDs) > 10 || !strings.Contains(g.Criterion, "owner=") {
			t.Fatal("unbounded or opaque group")
		}
		for _, id := range g.MutantIDs {
			if seen[id] {
				t.Fatal("duplicate member")
			}
			seen[id] = true
		}
	}
	for i, m := range results.Mutants {
		shifted, ok := m.(triageMutant)
		if !ok {
			t.Fatal("unexpected fixture")
		}
		shifted.position.Line += 10
		results.Mutants[i] = shifted
	}
	shifted, _ := renderJSON(t, results, report.Options{GroupReview: true})
	before, _ := json.Marshal(doc.ReviewGroups)
	after, _ := json.Marshal(shifted.ReviewGroups)
	if !bytes.Equal(before, after) {
		t.Fatal("line shift changed groups")
	}
	ungrouped, _ := renderJSON(t, results, report.Options{})
	if len(ungrouped.ReviewGroups) != 0 || len(ungrouped.Groups[mutator.Lived.String()]["source.go"]) != 23 {
		t.Fatal("cannot ungroup")
	}
}

func TestTriageFilterAuditAndWholeRunGates(t *testing.T) {
	results, ledger := triageResults()
	for _, jsonFormat := range []bool{true, false} {
		var out bytes.Buffer
		err := report.Write(&out, results, report.Options{JSON: jsonFormat, Triage: &ledger, HideReviewed: true, EfficacyThreshold: 50})
		if err == nil {
			t.Fatal("triage waived failing gate")
		}
		if jsonFormat {
			var doc internal.OutputResult
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.MutantsLived != 23 || doc.MutantsKilled != 0 || doc.TestEfficacy != 0 || doc.Triage.Hidden != 1 || doc.Triage.Reviewed != 1 || doc.Triage.Counts["suspected-equivalence"] != 1 || len(doc.Groups[mutator.Lived.String()]["source.go"]) != 22 {
				t.Fatalf("unauditable report: %+v", doc)
			}
			if doc.Triage.Ledger.Entries[0].Resolution != "matched" {
				t.Fatal("missing disposition")
			}
		} else if !strings.Contains(out.String(), "hidden=1") || !strings.Contains(out.String(), ledger.Entries[0].Reason) || strings.Contains(out.String(), "    1:4 ") {
			t.Fatalf("bad text: %s", out.String())
		}
	}
	// Status filtering cannot remove disposition audit records.
	doc, _ := renderJSON(t, results, report.Options{Triage: &ledger, HideReviewed: true, Statuses: []mutator.Status{mutator.Killed}})
	if doc.MutantsLived != 23 || len(doc.Triage.Ledger.Entries) != 1 || doc.Triage.Reviewed != 1 || doc.Triage.Hidden != 0 {
		t.Fatal("status filter lost audit")
	}
}

func TestTriageCannotWaiveCoverageGate(t *testing.T) {
	results, ledger := triageResults()
	for i, m := range results.Mutants {
		observed, ok := m.(triageMutant)
		if !ok {
			t.Fatal("unexpected fixture")
		}
		observed.status = mutator.NotCovered
		results.Mutants[i] = observed
	}
	var out bytes.Buffer
	err := report.Write(&out, results, report.Options{JSON: true, Triage: &ledger, HideReviewed: true, CoverageThreshold: 50})
	if err == nil {
		t.Fatal("triage waived coverage gate")
	}
	var doc internal.OutputResult
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.MutantsNotCovered != 23 || doc.MutationsCoverage != 0 || doc.Triage.Hidden != 1 {
		t.Fatalf("coverage audit changed: %+v", doc)
	}
}

type unknownOwnerMutant struct{ triageMutant }

func (m unknownOwnerMutant) ObservationContext() *mutator.ObservationContext { return nil }

func TestTriageGroupingLeavesUnknownAndAmbiguousFindingsStandalone(t *testing.T) {
	results, _ := triageResults()
	first, ok := results.Mutants[0].(triageMutant)
	if !ok {
		t.Fatal("unexpected fixture")
	}
	results.Mutants = []mutator.Mutator{first, first, unknownOwnerMutant{first}}
	doc, _ := renderJSON(t, results, report.Options{GroupReview: true})
	if len(doc.ReviewGroups) != 0 || len(doc.Groups[mutator.Lived.String()]["source.go"]) != 3 {
		t.Fatal("ambiguous or unknown findings lost")
	}
	results.Mutants = []mutator.Mutator{unknownOwnerMutant{first}}
	doc, _ = renderJSON(t, results, report.Options{GroupReview: true})
	if len(doc.ReviewGroups) != 0 || doc.MutantsTotal != 1 {
		t.Fatal("unknown owner grouped")
	}
}

func TestTriageGroupingExactlyTenMembersProducesOneNonemptyGroup(t *testing.T) {
	results, _ := triageResults()
	results.Mutants = results.Mutants[:10]
	doc, _ := renderJSON(t, results, report.Options{GroupReview: true})
	if len(doc.ReviewGroups) != 1 {
		t.Fatalf("exactly ten members must yield one group, got %+v", doc.ReviewGroups)
	}
	if len(doc.ReviewGroups[0].MutantIDs) != 10 {
		t.Fatalf("group lost members: %+v", doc.ReviewGroups[0])
	}
	seen := make(map[string]bool)
	for _, id := range doc.ReviewGroups[0].MutantIDs {
		if id == "" || seen[id] {
			t.Fatalf("invalid or repeated member %q", id)
		}
		seen[id] = true
	}
	if len(doc.Groups[mutator.Lived.String()]["source.go"]) != 10 {
		t.Fatal("grouping lost underlying findings")
	}
}
