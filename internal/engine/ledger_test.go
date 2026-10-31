package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

type ledgerExecutor struct {
	status   mutator.Status
	attempts int
	err      error
}

func (e *ledgerExecutor) Execute(_ context.Context, mutants []mutator.Mutator, completed func(mutator.Mutator)) ([]mutator.Mutator, error) {
	for _, m := range mutants {
		if m.Status() == mutator.Runnable {
			e.attempts++
			m.SetStatus(e.status)
		}
		if completed != nil {
			completed(m)
		}
	}

	return mutants, e.err
}
func TestEngineLedgerReuseCoverageTransitionsAndFailure(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.ArithmeticBase), true)
	source := "package calc\nfunc Sum(a,b int)int{return a+b}\n"
	options := ledger.Options{Path: filepath.Join(t.TempDir(), "ledger.json"), TTL: time.Hour, MaxEntries: 100, Fingerprint: func(context.Context) (string, error) { return "same-inputs", nil }}
	covered := coverage.Profile{"calc.go": {{StartLine: 2, EndLine: 2, StartCol: 1, EndCol: 100}}}
	execute := func(source string, data engine.CodeData, status mutator.Status, runErr error) (report.Results, int, error) {
		t.Helper()
		session := ledger.Open(context.Background(), options)
		defer session.Close()
		if session.Reason != "" {
			t.Fatal(session.Reason)
		}
		executor := &ledgerExecutor{status: status, err: runErr}
		fs := fstest.MapFS{"calc.go": &fstest.MapFile{Data: []byte(source)}}
		e := engine.New(gomodule.GoModule{Name: "example.com/ledger", CallingDir: "."}, data, executor, engine.WithDirFs(fs), engine.WithLedger(session))
		result, err := e.Run(context.Background())

		return result, executor.attempts, err
	}
	for i := 0; i < 3; i++ {
		result, attempts, err := execute(source, engine.CodeData{Cov: covered}, mutator.Killed, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantAttempts := 1
		provenance := "fresh"
		if i == 2 {
			wantAttempts = 0
			provenance = "reused"
		}
		if attempts != wantAttempts || len(result.Mutants) != 1 || result.Mutants[0].Status() != mutator.Killed || result.Evidence[0].Provenance != provenance {
			t.Fatal(attempts, result)
		}
	}
	// Coverage remains authoritative even if old conclusive evidence matches.
	result, attempts, err := execute(source, engine.CodeData{}, mutator.Killed, nil)
	if err != nil || attempts != 0 || result.Mutants[0].Status() != mutator.NotCovered || result.Evidence[0].Transition != "unresolved" {
		t.Fatal(result, attempts, err)
	}
	options.Fingerprint = func(context.Context) (string, error) { return "changed-tests", nil }
	// A lifecycle failure must not publish even completed-looking mutant results.
	before, err := os.ReadFile(options.Path)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("restoration failed")
	if _, _, err = execute(source, engine.CodeData{Cov: covered}, mutator.Killed, failure); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(options.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("engine published failed execution")
	}
	// Identity changes and removals are historical records, never current statuses.
	for _, tc := range []struct{ source, transition string }{
		{strings.ReplaceAll(source, "a+b", "a-b"), "changed"},
		{"package calc\n", "removed"},
	} {
		result, _, err = execute(tc.source, engine.CodeData{Cov: covered}, mutator.Killed, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range result.Evidence {
			if e.Provenance == "stale" && e.Transition == tc.transition {
				found = true
			}
		}
		if !found {
			t.Fatal("missing historical transition", tc.transition, result.Evidence)
		}
	}
}
