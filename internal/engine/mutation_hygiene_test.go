package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

func TestEngineUniqueSubtractionSettings(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		negative, arithmetic bool
		families             []mutator.Type
	}{
		{"both", true, true, []mutator.Type{mutator.InvertNegatives, mutator.ArithmeticBase}},
		{"negative only", true, false, []mutator.Type{mutator.InvertNegatives}},
		{"arithmetic only", false, true, []mutator.Type{mutator.ArithmeticBase}},
		{"neither", false, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{
				configuration.MutantTypeEnabledKey(mutator.InvertNegatives): tc.negative,
				configuration.MutantTypeEnabledKey(mutator.ArithmeticBase):  tc.arithmetic,
			})
			defer viperReset()
			source := []byte("package example\nfunc f(a,b int) int {\nreturn -a - b\n}\n")
			for _, status := range []mutator.Status{mutator.Runnable, mutator.NotCovered, mutator.Skipped} {
				data := engine.CodeData{}
				if status != mutator.NotCovered {
					data.Cov = coverage.Profile{"source.go": {{StartLine: 1, EndLine: 5}}}
				}
				if status == mutator.Skipped {
					data.Diff = diff.Diff{"source.go": {{StartLine: 1, EndLine: 1}}}
				}
				mu := engine.New(gomodule.GoModule{Name: "example.com", Root: ".", CallingDir: "."}, data, newJobDealerStub(t), engine.WithDirFs(fstest.MapFS{"source.go": {Data: source}}))
				results := runEngine(t, &mu)
				want := 2
				if len(tc.families) == 0 {
					want = 0
				}
				if len(results.Mutants) != want {
					t.Fatalf("got %d mutants, want %d", len(results.Mutants), want)
				}
				positions := make(map[int]bool)
				for _, m := range results.Mutants {
					if positions[m.Position().Offset] {
						t.Fatal("duplicate location")
					}
					positions[m.Position().Offset] = true
					if m.Type() != tc.families[0] || m.Status() != status {
						t.Fatalf("type/status = %s/%s", m.Type(), m.Status())
					}
					tm, ok := m.(*engine.TokenMutator)
					if !ok {
						t.Fatal("expected token mutant")
					}
					if !reflect.DeepEqual(tm.ContributingTypes(), tc.families) {
						t.Fatalf("families = %v", tm.ContributingTypes())
					}
					metadata := tm.ContributingTypes()
					metadata[0] = mutator.BooleanLiteral
					if tm.ContributingTypes()[0] != tc.families[0] {
						t.Fatal("metadata aliases internal slice")
					}
					offset := m.Position().Offset
					expected := string(source[:offset]) + "+" + string(source[offset+1:])
					if string(m.OrigSnippet()) != string(source) || string(m.MutatedSnippet()) != expected {
						t.Fatal("incorrect snippet")
					}
				}
				var out bytes.Buffer
				if err := report.Write(&out, results, report.Options{JSON: true, DryRun: true}); err != nil {
					t.Fatal(err)
				}
				var document struct {
					Total      int            `json:"mutants_total"`
					Statistics map[string]int `json:"mutator_statistics"`
				}
				if err := json.Unmarshal(out.Bytes(), &document); err != nil {
					t.Fatal(err)
				}
				if document.Total != want {
					t.Fatalf("report total = %d", document.Total)
				}
				if want > 0 && (len(document.Statistics) != 1 || document.Statistics[strings.ToLower(tc.families[0].String())] != want) {
					t.Fatalf("non-exclusive operator counts: %v", document.Statistics)
				}
			}
		})
	}
}

func TestEngineUniqueEditsExecuteAndRestore(t *testing.T) {
	viperSet(map[string]any{configuration.RunWorkersKey: 1})
	defer viperReset()
	source := []byte("package example\n// intentionally unformatted\nfunc f(a,b int)int{return -a-b}\n")
	original := t.TempDir()
	worker := t.TempDir()
	for _, dir := range []string{original, worker} {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com\n\ngo 1.21\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "source.go"), source, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dealer := newWdDealerStub(t)
	dealer.fnGet = func(string) (string, error) { return worker, nil }
	var calls atomic.Int32
	mod := gomodule.GoModule{Name: "example.com", Root: original, CallingDir: "."}
	executor := engine.NewExecutor(mod, dealer, time.Second, engine.WithExecContext(func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		calls.Add(1)
		// The command adapter observes the real isolated mutation before rollback.
		// #nosec G304 -- worker is a test-owned temporary directory.
		got, err := os.ReadFile(filepath.Join(worker, "source.go"))
		if err != nil || bytes.Equal(got, source) {
			t.Error("mutation was not applied")
		}

		return exec.CommandContext(ctx, "go", "version")
	}))
	mu := engine.New(mod, engine.CodeData{Cov: coverage.Profile{"source.go": {{StartLine: 1, EndLine: 5}}}}, executor)
	results := runEngine(t, &mu)
	if calls.Load() != 2 || len(results.Mutants) != 2 {
		t.Fatalf("executions=%d mutants=%d", calls.Load(), len(results.Mutants))
	}
	for _, m := range results.Mutants {
		if m.Status() != mutator.Lived {
			t.Fatalf("status=%s", m.Status())
		}
	}
	for _, dir := range []string{original, worker} {
		// #nosec G304 -- dir is a test-owned temporary directory.
		got, err := os.ReadFile(filepath.Join(dir, "source.go"))
		if err != nil || !bytes.Equal(got, source) {
			t.Fatalf("source not restored in %s: %q, %v", dir, got, err)
		}
	}
}

func TestEngineRemainderAssignmentHasNoIdentityEdit(t *testing.T) {
	for _, source := range []string{"package p\nfunc f(a int){a%=2}\n", "package p\n\nfunc f(a int) {\n\ta %= 2\n}\n"} {
		t.Run(source, func(t *testing.T) {
			viperSet(map[string]any{configuration.MutantTypeEnabledKey(mutator.InvertAssignments): true})
			defer viperReset()
			mu := engine.New(gomodule.GoModule{Name: "example.com", Root: ".", CallingDir: "."}, engine.CodeData{}, newJobDealerStub(t), engine.WithDirFs(fstest.MapFS{"source.go": {Data: []byte(source)}}))
			results := runEngine(t, &mu)
			if len(results.Mutants) != 1 || results.Mutants[0].Type() != mutator.RemoveSelfAssignments {
				t.Fatalf("expected only distinct assignment edit: %v", results.Mutants)
			}
			configuration.Set(configuration.MutantTypeEnabledKey(mutator.RemoveSelfAssignments), false)
			if got := runEngine(t, &mu); len(got.Mutants) != 0 {
				t.Fatal("identity edit discovered")
			}
		})
	}
}
