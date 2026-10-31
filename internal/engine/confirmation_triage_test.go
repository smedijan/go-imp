package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
	"github.com/smedijan/go-imp/internal/triage"
)

type reviewedConfirmationMutant struct {
	*confirmationMutant
	identity mutator.Identity
}

func (m *reviewedConfirmationMutant) Identity() mutator.Identity { return m.identity }

func TestExecutorConfirmationTriageHidingIsReportOnly(t *testing.T) {
	viperSet(map[string]any{configuration.RunConfirmOutcomesKey: true, configuration.RunWorkersKey: 1, configuration.RunHideReviewedKey: true})
	defer viperReset()
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "state"), []byte("pristine"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &reviewedConfirmationMutant{
		confirmationMutant: &confirmationMutant{evidenceMutant: &evidenceMutant{mutantStub: &mutantStub{status: mutator.Runnable, pkg: "example.test"}}},
		identity:           mutator.Identity{ID: "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), Version: 1, ModulePath: "source.go", Original: "+", Replacement: "-"},
	}
	ledger := triage.Ledger{Version: 1, Module: "example.test", Entries: []triage.Entry{{Identity: m.Identity(), Disposition: "accepted-gap", Reason: "reviewed", Provenance: "test reviewer"}}}
	input := filepath.Join(t.TempDir(), "triage.json")
	data, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	configuration.Set(configuration.RunTriageInputKey, input)
	dealer := workdir.NewCachedDealer(t.TempDir(), src)
	defer dealer.Clean()
	outcomes := []string{"fail", "pass", "fail"}
	attempts := 0
	executor := engine.NewExecutor(gomodule.GoModule{Root: src, CallingDir: "."}, dealer, time.Second,
		engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if attempts >= len(outcomes) {
				t.Fatal("confirmation exceeded attempt bound")
			}
			cmd := helperCommand(ctx, "TestConfirmationProcess", name, args...)
			cmd.Env = append(cmd.Env, "CONFIRM_RESULT="+outcomes[attempts], fmt.Sprintf("CONFIRM_INDEX=%d", attempts))
			attempts++

			return cmd
		}))
	mutants, err := executor.Execute(context.Background(), []mutator.Mutator{m}, nil)
	if err != nil || len(mutants) != 1 || attempts != 3 || m.Status() != mutator.Killed || !m.ExecutionEvidence().ReusableDetection() || len(m.ExecutionEvidence().Attempts) != 3 || m.applies != 2 || m.rollbacks != 2 {
		t.Fatalf("review suppressed execution/confirmation: results=%v err=%v attempts=%d evidence=%+v apply=%d rollback=%d", mutants, err, attempts, m.ExecutionEvidence(), m.applies, m.rollbacks)
	}
	assertConfirmedTriageReport(t, report.Results{Module: ledger.Module, Mutants: mutants}, ledger)
}

func assertConfirmedTriageReport(t *testing.T, results report.Results, ledger triage.Ledger) {
	t.Helper()
	var unfiltered map[string]json.RawMessage
	for _, hide := range []bool{false, true} {
		var out bytes.Buffer
		if err := report.Write(&out, results, report.Options{JSON: true, Triage: &ledger, HideReviewed: hide, EfficacyThreshold: 99, CoverageThreshold: 99}); err != nil {
			t.Fatal(err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		var audit struct{ Hidden, Reviewed int }
		if err := json.Unmarshal(doc["triage"], &audit); err != nil {
			t.Fatal(err)
		}
		var groups map[string]map[string][]json.RawMessage
		if err := json.Unmarshal(doc["groups"], &groups); err != nil {
			t.Fatal(err)
		}
		findings := 0
		for _, files := range groups {
			for _, entries := range files {
				findings += len(entries)
			}
		}
		if audit.Reviewed != 1 || (!hide && (audit.Hidden != 0 || findings != 1)) || (hide && (audit.Hidden != 1 || findings != 0)) {
			t.Fatalf("hide=%t audit=%+v findings=%d", hide, audit, findings)
		}
		if string(doc["mutants_total"]) != "1" || string(doc["mutants_killed"]) != "1" || string(doc["mutants_lived"]) != "0" || string(doc["test_efficacy"]) != "100" || string(doc["mutations_coverage"]) != "100" {
			t.Fatalf("incorrect whole-run counts: %s", out.String())
		}
		delete(doc, "groups")
		delete(doc, "triage")
		if !hide {
			unfiltered = doc
		} else if !reflect.DeepEqual(unfiltered, doc) {
			t.Fatal("hiding reviewed findings changed whole-run counts/metrics")
		}
	}
}
