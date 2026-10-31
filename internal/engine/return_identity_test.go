//nolint:gosec // Fixture paths are created under t.TempDir.
package engine_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

func discoverReturnIdentity(t *testing.T, source string, ids []string) report.Results {
	t.Helper()
	sourceFS := fstest.MapFS{"calc.go": {Data: []byte(source)}}
	e := engine.New(gomodule.GoModule{Name: "example.test/replay", CallingDir: "."}, engine.CodeData{TargetIDs: ids}, identityExecutor{}, engine.WithDirFs(sourceFS), engine.WithReturnAnalysisLoader(fixtureReturnLoader(t, sourceFS, types.SizesFor("gc", "amd64"))))
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	return result
}

func TestReturnIdentityDeterminismAndReplayResolution(t *testing.T) {
	returnTestConfig(t)
	source := "package calc\nfunc Keep(b,c bool,e error)(bool,error){return b && c,e}\n"
	initial := discoverReturnIdentity(t, source, nil)
	if len(initial.Mutants) != 3 {
		t.Fatalf("got %d return mutants, want 3", len(initial.Mutants))
	}
	ids := make([]string, 0, len(initial.Mutants))
	for _, m := range initial.Mutants {
		identity, ok := m.(mutator.Identified)
		if !ok {
			t.Fatal("return mutation has no identity")
		}
		edit := identity.Identity()
		if edit.ID == "" || edit.ModulePath != "calc.go" || edit.Original == "" || edit.Replacement == "" {
			t.Fatalf("incomplete return identity: %+v", edit)
		}
		if m.Type() == mutator.ErrorReturn && (edit.Original != "e" || edit.Replacement != "nil") {
			t.Fatalf("wrong error replacement: %+v", edit)
		}
		ids = append(ids, edit.ID)
	}
	for _, tc := range []struct {
		name, source, resolution string
		count                    int
	}{
		{"unchanged", source, "matched", 3},
		{"line-shift", "// unrelated header\n\n" + source, "matched", 3},
		{"expression-formatting", strings.Replace(source, "b && c", "b&& /* comment */ c", 1), "matched", 3},
		{"changed-expression", strings.Replace(source, "b && c", "c && b", 1), "changed", 0},
		{"removed-return", "package calc\nfunc Keep(b,c bool,e error)(flag bool,err error){return}\n", "changed", 0},
		{"deleted-declaration", "package calc\n", "missing", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := discoverReturnIdentity(t, tc.source, ids)
			if len(result.Mutants) != tc.count || len(result.Replay) != 3 {
				t.Fatalf("unexpected resolution: %+v", result)
			}
			for _, target := range result.Replay {
				if target.Resolution != tc.resolution {
					t.Fatalf("got %+v, want %s", target, tc.resolution)
				}
			}
		})
	}
}

func TestReturnIdentityDeduplicatesBooleanLiteralAlias(t *testing.T) {
	returnTestConfig(t)
	source := "package calc\nfunc Constant()bool{return true}\n"
	returns := discoverReturnIdentity(t, source, nil)
	if len(returns.Mutants) != 1 {
		t.Fatal("expected one return mutant")
	}
	id := identityID(returns.Mutants[0])
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.BooleanLiteral), true)
	both := discoverReturnIdentity(t, source, []string{id})
	if len(both.Mutants) != 1 || both.Replay[0].Resolution != "matched" {
		t.Fatalf("duplicate return/literal edit not canonical: %+v", both)
	}
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.BooleanReturn), false)
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.ErrorReturn), false)
	literal := discoverReturnIdentity(t, source, []string{id})
	if len(literal.Mutants) != 1 || literal.Mutants[0].Type() != mutator.BooleanLiteral || identityID(literal.Mutants[0]) != id {
		t.Fatal("alias selection changed canonical edit identity")
	}
}

func TestReturnIdentityReplayActuallyExecutesMutants(t *testing.T) {
	returnTestConfig(t)
	root, temp := t.TempDir(), t.TempDir()
	source := "package calc\nfunc Keep(b bool,e error)(bool,error){return b,e}\n"
	files := map[string]string{
		"go.mod":       "module example.test/replay\n\ngo 1.23\n",
		"calc.go":      source,
		"calc_test.go": "package calc\nimport(\"testing\";\"errors\")\nfunc TestKeep(t *testing.T){want:=errors.New(\"sentinel\");b,e:=Keep(true,want);if !b || e!=want {t.Fatal(\"return mutant detected\")}}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	baseline := exec.CommandContext(context.Background(), "go", "test", "-count=1", "./...")
	baseline.Dir = root
	if out, err := baseline.CombinedOutput(); err != nil {
		t.Fatalf("baseline: %s: %v", out, err)
	}
	prior := discoverReturnIdentity(t, source, nil)
	var requested []mutator.Mutator
	for _, m := range prior.Mutants {
		identity, ok := m.(mutator.Identified)
		if !ok {
			t.Fatal("missing identity")
		}
		if identity.Identity().Replacement != "true" {
			requested = append(requested, m)
		}
	}
	prior.Mutants = requested
	var document bytes.Buffer
	if err := report.Write(&document, prior, report.Options{JSON: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	ids, err := report.ReplayIDs(&document, "example.test/replay")
	if err != nil || len(ids) != 2 {
		t.Fatalf("prior-report selection: %v, %v", ids, err)
	}
	shifted := "// unrelated header\n\n" + source
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(shifted), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration.Set(configuration.RunDryRunKey, false)
	configuration.Set(configuration.RunWorkersKey, 1)
	mod := gomodule.GoModule{Name: "example.test/replay", Root: root, CallingDir: "."}
	dealer := workdir.NewCachedDealer(temp, root)
	defer dealer.Clean()
	e := engine.New(mod, engine.CodeData{TargetIDs: ids, Cov: coverage.Profile{"calc.go": {{StartLine: 1, StartCol: 1, EndLine: 99, EndCol: 999}}}}, engine.NewExecutor(mod, dealer, time.Second))
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Mutants) != 2 || len(result.Replay) != 2 {
		t.Fatalf("did not replay exact return set: %+v", result)
	}
	for _, m := range result.Mutants {
		rm, ok := m.(*engine.ReturnMutator)
		if !ok {
			t.Fatal("expected return mutant")
		}
		if rm.Status() != mutator.Killed || !rm.Applied() || len(rm.TestCommand()) == 0 {
			t.Fatalf("return mutant not executed: %s, applied=%v", rm.Status(), rm.Applied())
		}
		edit := rm.Identity()
		start := rm.Position().Offset
		payload := shifted[:start] + edit.Replacement + shifted[start+len(edit.Original):]
		if rm.AppliedSourceHash() != fmt.Sprintf("%x", sha256.Sum256([]byte(payload))) {
			t.Fatal("evidence does not identify actual return splice")
		}
		restored, err := os.ReadFile(filepath.Join(rm.Workdir(), "calc.go"))
		if err != nil || string(restored) != shifted {
			t.Fatalf("return worker not restored: %v", err)
		}
	}
	document.Reset()
	if err := report.Write(&document, result, report.Options{JSON: true}); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(document.Bytes(), []byte(`"applied":true`)) != 2 || bytes.Count(document.Bytes(), []byte(`"applied_source_sha256"`)) != 2 {
		t.Fatalf("missing return execution evidence: %s", document.Bytes())
	}
	// A stale report cannot execute after an expression edit in that declaration.
	changed := strings.Replace(shifted, "return b,e", "return !b,e", 1)
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := discoverReturnIdentity(t, changed, ids)
	if len(stale.Mutants) != 0 {
		t.Fatal("stale return report selected mutants")
	}
	for _, target := range stale.Replay {
		if target.Resolution != "changed" {
			t.Fatalf("stale return target: %+v", target)
		}
	}
}

func TestReturnIdentityRejectsChangedWorkerContext(t *testing.T) {
	returnTestConfig(t)
	source := "package calc\nfunc Keep(b bool)bool{return b}\n"
	result := discoverReturnIdentity(t, source, nil)
	rm, ok := result.Mutants[0].(*engine.ReturnMutator)
	if !ok {
		t.Fatal("expected return mutant")
	}
	root := t.TempDir()
	changed := source + "// changed after discovery\n"
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	rm.SetWorkdir(root)
	if err := rm.Apply(); err == nil || rm.Applied() {
		t.Fatal("accepted stale worker context around unchanged return expression")
	}
	if err := rm.Rollback(); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(root, "calc.go"))
	if err != nil || string(restored) != changed {
		t.Fatal("stale worker overwritten")
	}
}
