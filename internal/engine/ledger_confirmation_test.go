package engine_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/ledger"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

func runLedgerObservation(t *testing.T, options ledger.Options, strict bool, status mutator.Status, evidence *mutator.ExecutionEvidence) (report.Results, int) {
	t.Helper()
	configuration.Set(configuration.RunConfirmOutcomesKey, strict)
	session := ledger.Open(context.Background(), options)
	defer session.Close()
	if session.Reason != "" {
		t.Fatal(session.Reason)
	}
	executor := &ledgerExecutor{status: status, evidence: evidence}
	source := fstest.MapFS{"calc.go": &fstest.MapFile{Data: []byte("package calc\nfunc Sum(a,b int)int{return a+b}\n")}}
	covered := coverage.Profile{"calc.go": {{StartLine: 2, EndLine: 2, StartCol: 1, EndCol: 100}}}
	e := engine.New(gomodule.GoModule{Name: "example.com/ledger", CallingDir: "."}, engine.CodeData{Cov: covered}, executor, engine.WithDirFs(source), engine.WithLedger(session))
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if session.Reason != "" {
		t.Fatal(session.Reason)
	}

	return result, executor.attempts
}

func TestEngineLedgerConfirmationEligibility(t *testing.T) {
	enableIdentityOperators(t)
	for _, tc := range []struct {
		name     string
		strict   bool
		status   mutator.Status
		evidence *mutator.ExecutionEvidence
		reusable bool
	}{
		{"off-legacy-kill", false, mutator.Killed, nil, true},
		{"off-normal-survivor", false, mutator.Lived, &mutator.ExecutionEvidence{Category: "pass"}, true},
		{"off-known-unreliable", false, mutator.Killed, &mutator.ExecutionEvidence{Confidence: "inconsistent", Category: "test"}, false},
		{"strict-confirmed-kill", true, mutator.Killed, &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "test"}, true},
		{"strict-missing-evidence", true, mutator.Killed, nil, false},
		{"strict-unconfirmed-kill", true, mutator.Killed, &mutator.ExecutionEvidence{Category: "test"}, false},
		{"strict-inconsistent-kill", true, mutator.Killed, &mutator.ExecutionEvidence{Confidence: "inconsistent", Category: "test"}, false},
		{"strict-unstable-baseline", true, mutator.Killed, &mutator.ExecutionEvidence{Confidence: "baseline-unstable", Category: "test"}, false},
		{"strict-infrastructure", true, mutator.Killed, &mutator.ExecutionEvidence{Confidence: "infrastructure", Category: "test"}, false},
		{"strict-unresolved-timeout", true, mutator.Killed, &mutator.ExecutionEvidence{Confidence: "unresolved-timeout", Category: "test"}, false},
		{"strict-wrong-category", true, mutator.Killed, &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "timeout"}, false},
		{"strict-survivor", true, mutator.Lived, &mutator.ExecutionEvidence{Category: "pass"}, false},
		{"strict-timeout", true, mutator.TimedOut, &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "test"}, false},
		{"strict-notviable", true, mutator.NotViable, &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "test"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := ledger.Options{Path: filepath.Join(t.TempDir(), "ledger.json"), TTL: time.Hour, MaxEntries: 20, Fingerprint: func(context.Context) (string, error) { return "same-policy-inputs", nil }}
			for i := 0; i < 3; i++ {
				result, attempts := runLedgerObservation(t, options, tc.strict, tc.status, tc.evidence)
				reused := tc.reusable && i == 2
				expectedAttempts := 1
				provenance := "fresh"
				if reused {
					expectedAttempts = 0
					provenance = "reused"
				}
				if len(result.Mutants) != 1 || result.Mutants[0].Status() != tc.status || attempts != expectedAttempts || result.Evidence[0].Provenance != provenance {
					t.Fatal("current status or reuse eligibility changed", attempts, result)
				}
				observed, ok := result.Mutants[0].(interface {
					ExecutionEvidence() *mutator.ExecutionEvidence
				})
				if !ok {
					t.Fatal("current observation interface lost")
				}
				if reused {
					if observed.ExecutionEvidence() != nil {
						t.Fatal("reused finding fabricated current observation")
					}
				} else if observed.ExecutionEvidence() != tc.evidence {
					t.Fatal("current observation altered by ledger eligibility")
				}
			}
		})
	}
}

func TestEngineLedgerUncertaintyReplacesHistoricalDetection(t *testing.T) {
	enableIdentityOperators(t)
	fingerprint := "original-inputs"
	options := ledger.Options{Path: filepath.Join(t.TempDir(), "ledger.json"), TTL: time.Hour, MaxEntries: 20, Fingerprint: func(context.Context) (string, error) { return fingerprint, nil }}
	confirmed := &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "test"}
	first, _ := runLedgerObservation(t, options, true, mutator.Killed, confirmed)
	runLedgerObservation(t, options, true, mutator.Killed, confirmed)
	id := identityID(first.Mutants[0])
	fingerprint = "changed-inputs"
	result, attempts := runLedgerObservation(t, options, true, mutator.Killed, &mutator.ExecutionEvidence{Confidence: "inconsistent", Category: "test"})
	if attempts != 1 || result.Mutants[0].Status() != mutator.Killed || result.Evidence[0].Transition != "unresolved" {
		t.Fatal(result, attempts)
	}
	for _, value := range []string{"original-inputs", "changed-inputs"} {
		fingerprint = value
		s := ledger.Open(context.Background(), options)
		if _, ok := s.ReuseKilled(id); ok {
			t.Fatal("uncertainty retained a reusable historical detection")
		}
		s.Close()
	}
}
