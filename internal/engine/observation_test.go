package engine_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/exclusion"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

func observationOf(t *testing.T, m mutator.Mutator) *mutator.ObservationContext {
	t.Helper()
	provider, ok := m.(mutator.ObservationProvider)
	if !ok || provider.ObservationContext() == nil {
		t.Fatal("missing discovery observation")
	}

	return provider.ObservationContext()
}

func TestObservationContextParsedOwners(t *testing.T) {
	viperSet(map[string]any{})
	defer viperReset()
	source := `package owners
 type Receiver struct{}
 var A,B=1+2,3+4
 func (r *Receiver) Add(a int) int {return a+1}
 func Outer(a int) int { f:=func() int{return a+2}; return f() }
 `
	results := discoverIdentity(t, source, engine.CodeData{}, ".")
	got := map[string]int{}
	for _, m := range results.Mutants {
		observation := observationOf(t, m)
		if observation.Package.Value != "owners" || !observation.Package.Known || observation.Package.Truncated || observation.PackagePath.Known || observation.Selection.ImpactProvenance != "unknown" {
			t.Fatalf("unproven owner/provenance: %+v", observation)
		}
		if !observation.EnclosingSymbol.Known || observation.EnclosingSymbol.Truncated {
			t.Fatalf("missing owner: %+v", observation)
		}
		got[observation.EnclosingSymbol.Value]++
	}
	if got["var:A:B"] != 2 || got["func:/Receiver:Add"] != 1 || got["func::Outer"] != 1 || len(got) != 3 {
		t.Fatalf("wrong enclosing declarations: %v", got)
	}
}

func TestObservationContextBoundedUnicodeLabels(t *testing.T) {
	viperSet(map[string]any{})
	defer viperReset()
	name := strings.Repeat("界", 400)
	results := discoverIdentity(t, "package "+name+"\nfunc "+name+"(a int) int {return a+1}\n", engine.CodeData{}, ".")
	if len(results.Mutants) != 1 {
		t.Fatal("expected one candidate")
	}
	observation := observationOf(t, results.Mutants[0])
	for _, label := range []mutator.ObservedName{observation.Package, observation.EnclosingSymbol} {
		if !label.Known || !label.Truncated || len(label.Value) > 1024 || !utf8.ValidString(label.Value) {
			t.Fatalf("unbounded/invalid source label: %+v", label)
		}
	}
}

func TestObservationContextEligibilityDecisions(t *testing.T) {
	source := "package calc\nfunc Add(a int) int {return a+1}\n"
	covered := coverage.Profile{"calc.go": {{StartLine: 1, EndLine: 5, StartCol: 1, EndCol: 100}}}
	for _, tc := range []struct {
		name                   string
		cov                    coverage.Profile
		changes                diff.Diff
		dry                    bool
		status                 mutator.Status
		reason, coverage, diff string
	}{
		{name: "covered", cov: covered, status: mutator.Runnable, reason: "covered-and-in-scope", coverage: "covered", diff: "unrestricted"},
		{name: "uncovered", status: mutator.NotCovered, reason: "not-covered", coverage: "not-covered", diff: "unrestricted"},
		{name: "changed", cov: covered, changes: diff.Diff{"calc.go": {{StartLine: 2, EndLine: 2}}}, status: mutator.Runnable, reason: "covered-and-in-scope", coverage: "covered", diff: "changed"},
		{name: "outside diff", cov: covered, changes: diff.Diff{"other.go": {{StartLine: 1, EndLine: 9}}}, status: mutator.Skipped, reason: "outside-diff", coverage: "covered", diff: "outside-diff"},
		{name: "dry run", cov: covered, dry: true, status: mutator.Runnable, reason: "dry-run", coverage: "covered", diff: "unrestricted"},
		{name: "uncovered dry run", dry: true, status: mutator.NotCovered, reason: "not-covered", coverage: "not-covered", diff: "unrestricted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunDryRunKey: tc.dry})
			defer viperReset()
			results := discoverIdentity(t, source, engine.CodeData{Cov: tc.cov, Diff: tc.changes}, ".")
			if len(results.Mutants) != 1 {
				t.Fatal("expected one candidate")
			}
			m := results.Mutants[0]
			selection := observationOf(t, m).Selection
			if m.Status() != tc.status || selection.Coverage != tc.coverage || selection.Diff != tc.diff || selection.EligibilityReason != tc.reason || selection.DryRun != tc.dry || selection.Discovery != "syntax-only" || selection.File != "included" || selection.Replay != "not-requested" || selection.ImpactProvenance != "unknown" {
				t.Fatalf("status=%s selection=%+v", m.Status(), selection)
			}
		})
	}
}

func TestObservationContextReplayAndExclusions(t *testing.T) {
	viperSet(map[string]any{configuration.RunDryRunKey: true, configuration.RunExcludeFiles: []string{"excluded.go"}})
	defer viperReset()
	source := "package calc\nfunc Add(a int) int {return a+1}\n"
	prior := discoverIdentity(t, source, engine.CodeData{}, ".")
	id := identityID(prior.Mutants[0])
	replay := discoverIdentity(t, source, engine.CodeData{TargetIDs: []string{id}}, ".")
	if len(replay.Mutants) != 1 || observationOf(t, replay.Mutants[0]).Selection.Replay != "matched" {
		t.Fatal("missing exact replay observation")
	}
	unresolved := discoverIdentity(t, source, engine.CodeData{TargetIDs: []string{id, "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)}}, ".")
	if len(unresolved.Mutants) != 0 {
		t.Fatal("unresolved replay fabricated selected observations")
	}
	rules, err := exclusion.New()
	if err != nil {
		t.Fatal(err)
	}
	filesystem := fstest.MapFS{"calc.go": {Data: []byte(source)}, "excluded.go": {Data: []byte("package calc\nfunc Sub(a int)int{return a-1}\n")}}
	e := engine.New(gomodule.GoModule{Name: "example.test/replay", CallingDir: "."}, engine.CodeData{Exclusion: rules}, identityExecutor{}, engine.WithDirFs(filesystem))
	results, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Mutants) != 1 || results.Mutants[0].Position().Filename != "calc.go" || observationOf(t, results.Mutants[0]).Selection.File != "included" {
		t.Fatalf("exclusion observations invented candidates: %+v", results)
	}
}

func TestObservationContextActualDryRunMode(t *testing.T) {
	viperSet(map[string]any{configuration.RunDryRunKey: true})
	defer viperReset()
	mod := gomodule.GoModule{Name: "example.test/replay", CallingDir: "."}
	executor := engine.NewExecutor(mod, newWdDealerStub(t), time.Second)
	configuration.Set(configuration.RunDryRunKey, false)
	filesystem := fstest.MapFS{"calc.go": {Data: []byte("package calc\nfunc Add(a int) int{return a+1}\n")}}
	e := engine.New(mod, engine.CodeData{Cov: coverage.Profile{"calc.go": {{StartLine: 1, EndLine: 3, StartCol: 1, EndCol: 99}}}}, executor, engine.WithDirFs(filesystem))
	result, err := e.Run(context.Background())
	if err != nil || len(result.Mutants) != 1 {
		t.Fatalf("dry-run execution failed: %+v %v", result, err)
	}
	selection := observationOf(t, result.Mutants[0]).Selection
	if !selection.DryRun || selection.EligibilityReason != "dry-run" || result.Mutants[0].Status() != mutator.Runnable {
		t.Fatalf("reported configuration instead of actual executor decision: %+v", selection)
	}
}
