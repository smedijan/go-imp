package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestEngineActiveReplayCallingDirectoryIdentity(t *testing.T) {
	viperSet(map[string]any{configuration.RunDryRunKey: true})
	defer viperReset()
	root := writePackageFiles(t, map[string]string{
		"scope/source.go": "package sample\nfunc Add(a, b int) int { return a + b }\nfunc Keep(b bool) bool { return b }\n",
	})
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.BooleanReturn), true)
	var ids []string
	for _, callingDir := range []string{".", "scope"} {
		mod := gomodule.GoModule{Name: "example.com/discovery", Root: root, CallingDir: callingDir}
		e := engine.New(mod, engine.CodeData{}, newJobDealerStub(t))
		initial := runEngine(t, &e)
		if len(initial.Mutants) != 3 {
			t.Fatalf("%s: got %d mutants, want arithmetic and two Boolean returns", callingDir, len(initial.Mutants))
		}
		var gotIDs []string
		for _, m := range initial.Mutants {
			identified, ok := m.(mutator.Identified)
			if !ok || identified.Identity().ModulePath != "scope/source.go" {
				t.Fatalf("%s: missing module-relative identity at %s", callingDir, m.Position())
			}
			gotIDs = append(gotIDs, identityID(m))
		}
		if ids == nil {
			ids = gotIDs
		}
		// Replay root-discovered IDs through the production subtree loader.
		e = engine.New(mod, engine.CodeData{TargetIDs: ids}, newJobDealerStub(t))
		replayed := runEngine(t, &e)
		if len(replayed.Mutants) != len(ids) || len(replayed.Replay) != len(ids) {
			t.Fatalf("%s: replay did not select the same set: %+v", callingDir, replayed)
		}
		for _, target := range replayed.Replay {
			if target.Resolution != "matched" {
				t.Fatalf("%s: unstable identity: %+v", callingDir, target)
			}
		}
	}
}

func TestEngineActiveReplayUnresolvedSetExecutesNothing(t *testing.T) {
	for _, tc := range []struct {
		name, source, resolution string
		targetCount              int
		deactivate               func(*testing.T, string)
	}{
		{
			name: "inactive-tag", resolution: "missing", targetCount: 3,
			source:     "//go:build chosen\n\npackage sample\nfunc Target(a, b int) int { return a + b }\nfunc Keep(b bool) bool { return b }\n",
			deactivate: func(_ *testing.T, _ string) { configuration.Set(configuration.RunTagsKey, "") },
		},
		{
			name: "applicability-filtered", resolution: "changed", targetCount: 1,
			source: "package sample\nfunc Target(a, b Operand) Operand { return a + b }\n",
			deactivate: func(t *testing.T, root string) {
				t.Helper()
				// Only the type outside Target changes; its declaration tokens and ID locator remain intact.
				if err := os.WriteFile(filepath.Join(root, "operand.go"), []byte("package sample\ntype Operand = string\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunDryRunKey: false, configuration.RunTagsKey: "chosen"})
			defer viperReset()
			configuration.Set(configuration.MutantTypeEnabledKey(mutator.BooleanReturn), true)
			root := writePackageFiles(t, map[string]string{
				"target.go":  tc.source,
				"operand.go": "package sample\ntype Operand = int\n",
				"active.go":  "package sample\nfunc Active(a, b int) int { return a + b }\n",
			})
			mod := gomodule.GoModule{Name: "example.com/discovery", Root: root, CallingDir: "."}
			cov := coverage.Profile{}
			for _, file := range []string{"target.go", "active.go"} {
				cov[file] = []coverage.Block{{StartLine: 1, StartCol: 1, EndLine: 99, EndCol: 999}}
			}
			priorExecutor := newJobDealerStub(t)
			e := engine.New(mod, engine.CodeData{Cov: cov}, priorExecutor)
			prior := runEngine(t, &e)
			var activeID string
			var targetIDs []string
			for _, m := range prior.Mutants {
				if m.Status() != mutator.Runnable || identityID(m) == "" {
					t.Fatalf("expected covered identified candidate: %s", m.Position())
				}
				switch m.Position().Filename {
				case "active.go":
					activeID = identityID(m)
				case "target.go":
					targetIDs = append(targetIDs, identityID(m))
				}
			}
			if activeID == "" || len(targetIDs) != tc.targetCount || len(priorExecutor.gotMutants) != len(prior.Mutants) {
				t.Fatal("fixture did not exercise execution of both active and target candidates")
			}
			tc.deactivate(t, root)
			requested := append([]string{activeID}, targetIDs...)
			executor := newJobDealerStub(t)
			e = engine.New(mod, engine.CodeData{Cov: cov, TargetIDs: requested}, executor)
			replayed, err := e.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := []mutator.ReplayTarget{{ID: activeID, Resolution: "matched"}}
			for _, id := range targetIDs {
				want = append(want, mutator.ReplayTarget{ID: id, Resolution: tc.resolution})
			}
			if !reflect.DeepEqual(replayed.Replay, want) {
				t.Fatalf("replay = %+v, want %+v", replayed.Replay, want)
			}
			if len(replayed.Mutants) != 0 || len(executor.gotMutants) != 0 {
				t.Fatalf("partial replay executed: results=%v executed=%v", replayed.Mutants, executor.gotMutants)
			}
		})
	}
}
