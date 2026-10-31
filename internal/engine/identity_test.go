package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/exclusion"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

type identityExecutor struct{}

func (identityExecutor) Execute(_ context.Context, mutants []mutator.Mutator, _ func(mutator.Mutator)) ([]mutator.Mutator, error) {
	return mutants, nil
}

func enableIdentityOperators(t *testing.T) {
	t.Helper()
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	for _, operator := range mutator.Types {
		configuration.Set(configuration.MutantTypeEnabledKey(operator), operator != mutator.BooleanReturn && operator != mutator.ErrorReturn)
	}
}

func discoverIdentity(t *testing.T, source string, data engine.CodeData, callingDir string) report.Results {
	t.Helper()
	fs := fstest.MapFS{"calc.go": &fstest.MapFile{Data: []byte(source)}}
	e := engine.New(gomodule.GoModule{Name: "example.test/replay", CallingDir: callingDir}, data, identityExecutor{}, engine.WithDirFs(fs))
	results, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	return results
}

func identityID(m mutator.Mutator) string {
	identified, ok := m.(mutator.Identified)
	if !ok {
		return ""
	}

	return identified.Identity().ID
}

func TestIdentityDeterminismAliasesAndContext(t *testing.T) {
	enableIdentityOperators(t)
	source := "package calc\nfunc Sub(a,b int) int { return a-b }\n"
	first := discoverIdentity(t, source, engine.CodeData{}, ".")
	if len(first.Mutants) != 1 {
		t.Fatalf("aliases not deduplicated: %d", len(first.Mutants))
	}
	id := identityID(first.Mutants[0])
	if err := report.ValidateMutantIDs([]string{id}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{source, "// unrelated header\n\n" + source, strings.ReplaceAll(source, "a-b", "a - b")} {
		got := discoverIdentity(t, source, engine.CodeData{}, ".")
		if identityID(got.Mutants[0]) != id {
			t.Fatal("identity changed after whitespace/line shift")
		}
	}
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.InvertNegatives), false)
	alias := discoverIdentity(t, source, engine.CodeData{}, ".")
	if identityID(alias.Mutants[0]) != id {
		t.Fatal("operator alias changed identity")
	}
	nested := discoverIdentity(t, source, engine.CodeData{}, "sub")
	if identityID(nested.Mutants[0]) == id {
		t.Fatal("module-relative source path absent")
	}
	// Same module-relative path discovered from a different calling directory.
	fs := fstest.MapFS{"sub/calc.go": &fstest.MapFile{Data: []byte(source)}}
	e := engine.New(gomodule.GoModule{Name: "example.test/replay", CallingDir: "."}, engine.CodeData{}, identityExecutor{}, engine.WithDirFs(fs))
	res, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if identityID(res.Mutants[0]) != identityID(nested.Mutants[0]) {
		t.Fatal("calling directory changed identity")
	}
}

func TestIdentityReplayResolution(t *testing.T) {
	enableIdentityOperators(t)
	source := "package calc\nfunc Sub(a,b int) int { return a-b }\n"
	original := discoverIdentity(t, source, engine.CodeData{}, ".")
	id := identityID(original.Mutants[0])
	for _, tc := range []struct {
		name, source, resolution string
		count                    int
	}{
		{"line-shift", "// header\n" + source, "matched", 1},
		{"operand-edit", strings.ReplaceAll(source, "a-b", "b-a"), "changed", 0},
		{"operator-edit", strings.ReplaceAll(source, "a-b", "a+b"), "changed", 0},
		{"no-remaining-mutations", strings.ReplaceAll(source, "a-b", "a"), "changed", 0},
		{"deletion", "package calc\n", "missing", 0},
		{"rename", strings.ReplaceAll(source, "Sub", "Other"), "missing", 0},
		{"duplicate-declaration", source + "func Sub(a,b int) int { return a-b }\n", "ambiguous", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := discoverIdentity(t, tc.source, engine.CodeData{TargetIDs: []string{id, id}}, ".")
			if len(got.Replay) != 1 || got.Replay[0].Resolution != tc.resolution || len(got.Mutants) != tc.count {
				t.Fatalf("unexpected replay: %+v, mutants %d", got.Replay, len(got.Mutants))
			}
		})
	}
	// An exact set with one stale target executes none, even with a match.
	other := discoverIdentity(t, "package calc\nfunc Other(a,b int) int { return a+b }", engine.CodeData{}, ".")
	got := discoverIdentity(t, source, engine.CodeData{TargetIDs: []string{id, identityID(other.Mutants[0])}}, ".")
	if len(got.Mutants) != 0 || len(got.Replay) != 2 {
		t.Fatal("partial exact-set replay")
	}
}

func TestIdentityReplayCoverageDiffAndExclusions(t *testing.T) {
	enableIdentityOperators(t)
	source := "package calc\nfunc Sub(a,b int) int { return a-b }\n"
	id := identityID(discoverIdentity(t, source, engine.CodeData{}, ".").Mutants[0])
	got := discoverIdentity(t, source, engine.CodeData{TargetIDs: []string{id}}, ".")
	if got.Replay[0].Resolution != "matched" || got.Mutants[0].Status() != mutator.NotCovered {
		t.Fatal("replay bypassed coverage")
	}
	got = discoverIdentity(t, source, engine.CodeData{TargetIDs: []string{id}, Diff: diff.Diff{"other.go": {{StartLine: 1, EndLine: 99}}}}, ".")
	if got.Mutants[0].Status() != mutator.Skipped {
		t.Fatal("replay bypassed diff")
	}
	configuration.Set(configuration.RunExcludeFiles, []string{"calc.go"})
	rules, err := exclusion.New()
	if err != nil {
		t.Fatal(err)
	}
	got = discoverIdentity(t, source, engine.CodeData{TargetIDs: []string{id}, Exclusion: rules}, ".")
	if len(got.Mutants) != 0 || got.Replay[0].Resolution != "missing" {
		t.Fatal("replay bypassed exclusions")
	}
}

func TestIdentityReplayActuallyExecutesMutant(t *testing.T) {
	enableIdentityOperators(t)
	root, temp := t.TempDir(), t.TempDir()
	source := "package calc\nfunc Sub(a,b int) int { return a-b }\n"
	files := map[string]string{
		"go.mod":       "module example.test/replay\n\ngo 1.21\n",
		"calc.go":      source,
		"calc_test.go": "package calc\nimport \"testing\"\nfunc TestSub(t *testing.T) { if Sub(4,2) != 2 { t.Fatal(\"mutant detected\") } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	baseline := exec.CommandContext(context.Background(), "go", "test", "-count=1", "./...")
	baseline.Dir = root
	if out, err := baseline.CombinedOutput(); err != nil {
		t.Fatalf("baseline failed: %s: %v", out, err)
	}
	prior := discoverIdentity(t, source, engine.CodeData{}, ".")
	var document bytes.Buffer
	if err := report.Write(&document, prior, report.Options{JSON: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	ids, err := report.ReplayIDs(&document, "example.test/replay")
	if err != nil {
		t.Fatal(err)
	}
	// Replay a prior report after a real line shift in the source file.
	shifted := "// unrelated header\n\n//line generated.go:100\n" + source
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(shifted), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration.Set(configuration.RunWorkersKey, 1)
	mod := gomodule.GoModule{Name: "example.test/replay", Root: root, CallingDir: "."}
	dealer := workdir.NewCachedDealer(temp, root)
	defer dealer.Clean()
	// Allow cold Go compilation on Windows CI; this is not a timeout test.
	executor := engine.NewExecutor(mod, dealer, 15*time.Second)
	data := engine.CodeData{TargetIDs: ids, Cov: coverage.Profile{"calc.go": {{StartLine: 1, EndLine: 99, StartCol: 1, EndCol: 999}}}}
	e := engine.New(mod, data, executor)
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Mutants) != 1 || result.Mutants[0].Status() != mutator.Killed {
		t.Fatalf("did not execute exact mutant: %+v", result)
	}
	mutant, ok := result.Mutants[0].(*engine.TokenMutator)
	if !ok {
		t.Fatal("expected token mutant")
	}
	if !mutant.Applied() || len(mutant.TestCommand()) == 0 || identityID(mutant) != ids[0] {
		t.Fatal("missing execution evidence")
	}
	// Directly inspect the worker copy after rollback, not just the original module.
	restored, err := os.ReadFile(filepath.Join(mutant.Workdir(), "calc.go"))
	if err != nil || string(restored) != shifted {
		t.Fatalf("worker not restored: %s: %v", restored, err)
	}
	// #nosec G304 -- test-owned source in a temporary directory.
	original, err := os.ReadFile(filepath.Join(root, "calc.go"))
	if err != nil || string(original) != shifted {
		t.Fatal("original source changed")
	}
	document.Reset()
	if err := report.Write(&document, result, report.Options{JSON: true}); err != nil {
		t.Fatal(err)
	}
	var output struct {
		Groups map[string]map[string][]struct {
			ID          string
			Applied     bool
			TestCommand string `json:"test_command"`
			Original    string `json:"original_snippet"`
			Mutated     string `json:"mutated_snippet"`
		}
	}
	if err := json.Unmarshal(document.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	finding := output.Groups["KILLED"]["calc.go"][0]
	if finding.ID != ids[0] || !finding.Applied || finding.TestCommand == "" || finding.Original == finding.Mutated {
		t.Fatalf("missing JSON execution evidence: %+v", finding)
	}
}

func TestIdentityRejectsWorkerSourceChange(t *testing.T) {
	enableIdentityOperators(t)
	result := discoverIdentity(t, "package calc\nfunc Sub(a,b int) int { return a-b }", engine.CodeData{}, ".")
	mutant, ok := result.Mutants[0].(*engine.TokenMutator)
	if !ok {
		t.Fatal("expected token mutant")
	}
	root := t.TempDir()
	mutant.SetWorkdir(root)
	changed := []byte("package calc\nfunc Other(a,b int) int { return a+b }")
	if err := os.WriteFile(filepath.Join(root, "calc.go"), changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mutant.Apply(); err == nil || mutant.Applied() {
		t.Fatal("stale worker source accepted")
	}
	if err := mutant.Rollback(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- test-owned source in a temporary directory.
	restored, err := os.ReadFile(filepath.Join(root, "calc.go"))
	if err != nil || !bytes.Equal(restored, changed) {
		t.Fatal("stale worker source overwritten")
	}
}

func TestIdentityDistinguishesTokenIndexesAndLiteralContent(t *testing.T) {
	enableIdentityOperators(t)
	source := "package calc\nfunc Sum(a,b int)int{return a+b+b}\n"
	got := discoverIdentity(t, source, engine.CodeData{}, ".")
	if len(got.Mutants) != 2 || identityID(got.Mutants[0]) == identityID(got.Mutants[1]) {
		t.Fatal("distinct token indexes collapsed")
	}
	selected := discoverIdentity(t, source, engine.CodeData{TargetIDs: []string{identityID(got.Mutants[1])}}, ".")
	if len(selected.Mutants) != 1 || selected.Mutants[0].Position().Offset != got.Mutants[1].Position().Offset {
		t.Fatal("ID did not select exact repeated expression token")
	}
	for _, text := range []string{"`a b`", "`a  b`", "`a\nb`", "\"a b\""} {
		first := discoverIdentity(t, "package calc\nfunc F()bool{return "+text+"==`a b`}\n", engine.CodeData{}, ".")
		changed := discoverIdentity(t, "package calc\nfunc F()bool{return "+text+"==`different`}\n", engine.CodeData{}, ".")
		if identityID(first.Mutants[0]) == identityID(changed.Mutants[0]) {
			t.Fatal("literal content omitted from context")
		}
	}
}
