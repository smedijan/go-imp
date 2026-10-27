package report_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/smedijan/go-imp/internal/execution"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

func TestThresholdBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		killed, lived, uncovered, runnable, skipped int
		efficacy, coverage                          float64
		dryRun                                      bool
		code                                        int
	}{
		{name: "disabled", lived: 1, uncovered: 1},
		{name: "efficacy above", killed: 3, lived: 1, efficacy: 74.99},
		{name: "efficacy equal", killed: 3, lived: 1, efficacy: 75, code: 10},
		{name: "efficacy below", killed: 3, lived: 1, efficacy: 75.01, code: 10},
		{name: "coverage above", killed: 2, lived: 1, uncovered: 1, coverage: 74.99},
		{name: "coverage equal", killed: 2, lived: 1, uncovered: 1, coverage: 75, code: 11},
		{name: "coverage below", killed: 2, lived: 1, uncovered: 1, coverage: 75.01, code: 11},
		{name: "efficacy checked first", lived: 1, uncovered: 1, efficacy: 80, coverage: 90, code: 10},
		{name: "empty", efficacy: 99, coverage: 99},
		{name: "nothing in diff scope", skipped: 2, efficacy: 99, coverage: 99},
		{name: "dry run", runnable: 3, uncovered: 1, efficacy: 99, coverage: 99, dryRun: true},
	} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, jsonOutput), func(t *testing.T) {
				var mutants []mutator.Mutator
				for status, count := range map[mutator.Status]int{mutator.Killed: tc.killed, mutator.Lived: tc.lived, mutator.NotCovered: tc.uncovered, mutator.Runnable: tc.runnable, mutator.Skipped: tc.skipped} {
					for range count {
						mutants = append(mutants, stubMutant{status: status})
					}
				}
				var out bytes.Buffer
				err := report.Write(&out, report.Results{Mutants: mutants}, report.Options{
					JSON: jsonOutput, DryRun: tc.dryRun, EfficacyThreshold: tc.efficacy, CoverageThreshold: tc.coverage,
					Statuses: []mutator.Status{mutator.Skipped}, // No visible findings must not change any gate.
				})
				if tc.code == 0 {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var gate *execution.ExitError
					if !errors.As(err, &gate) || gate.ExitCode() != tc.code {
						t.Fatalf("wanted exit%d, got %v", tc.code, err)
					}
				}
				if out.Len() == 0 {
					t.Fatal("report not emitted")
				}
				if jsonOutput && !json.Valid(out.Bytes()) {
					t.Fatalf("incomplete JSON on threshold failure: %s", &out)
				}
			})
		}
	}
}
