package report_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

func TestEveryStatusAggregateCount(t *testing.T) {
	statuses := []mutator.Status{mutator.Lived, mutator.NotCovered, mutator.TimedOut, mutator.Runnable, mutator.Killed, mutator.Skipped, mutator.NotViable}
	for _, status := range statuses {
		for _, count := range []int{0, 1, 3} {
			t.Run(fmt.Sprintf("%s/count=%d", status, count), func(t *testing.T) {
				var mutants []mutator.Mutator
				for range count {
					mutants = append(mutants, stubMutant{status: status})
				}
				// Deliberately hide all but survivors: totals for every other status must remain.
				doc, _ := renderJSON(t, report.Results{Mutants: mutants}, report.Options{Statuses: []mutator.Status{mutator.Lived}})
				got := map[mutator.Status]int{
					mutator.Lived: doc.MutantsLived, mutator.NotCovered: doc.MutantsNotCovered, mutator.TimedOut: doc.MutantsTimedOut,
					mutator.Runnable: doc.MutantsRunnable, mutator.Killed: doc.MutantsKilled, mutator.Skipped: doc.MutantsSkipped, mutator.NotViable: doc.MutantsNotViable,
				}
				want := make(map[mutator.Status]int)
				for _, s := range statuses {
					want[s] = 0
				}
				want[status] = count
				if diff := cmp.Diff(want, got); diff != "" {
					t.Error(diff)
				}
				if doc.MutantsTotal != count {
					t.Errorf("total=%d, want %d", doc.MutantsTotal, count)
				}
			})
		}
	}
}
