package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestSnippetPreviews(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   string
		original string
		mutated  string
		snippet  string
		kind     mutator.Type
	}{
		{name: "boundary with longer token", source: "func f(a,b int)bool{return a>b}", original: "a>b", mutated: "a>=b", kind: mutator.ConditionalsBoundary},
		{name: "boundary with shorter token", source: "func f(a,b int)bool{return a>=b}", original: "a>=b", mutated: "a>b", kind: mutator.ConditionalsBoundary},
		{name: "unary", source: "func f(a int)int{return -a}", original: "-a", mutated: "+a", kind: mutator.InvertNegatives},
		{name: "assignment", source: "func f(a int){a+=2}", original: "a+=2", mutated: "a=2", kind: mutator.RemoveSelfAssignments},
		{name: "increment", source: "func f(a int){a++}", original: "a++", mutated: "a--", kind: mutator.IncrementDecrement},
		{name: "branch", source: "func f(){for{break}}", original: "break", mutated: "continue", kind: mutator.InvertLoopCtrl},
		{name: "multiline and Unicode", source: "// café\nfunc f(a,b int) bool {\n\n\n\nreturn a>b\n}", original: "a>b", mutated: "a>=b", snippet: "\n\n\nreturn a>b\n}\n", kind: mutator.ConditionalsBoundary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunDryRunKey: true})
			defer viperReset()
			source := []byte("package example\n" + tc.source + "\n")
			mod := gomodule.GoModule{Name: "example.com", Root: ".", CallingDir: "."}
			fs := fstest.MapFS{"source.go": {Data: source}}
			mu := engine.New(mod, engine.CodeData{}, newJobDealerStub(t), engine.WithDirFs(fs))
			result := runEngine(t, &mu)
			var found mutator.Mutator
			for _, m := range result.Mutants {
				if m.Type() == tc.kind {
					found = m

					break
				}
			}
			if found == nil {
				t.Fatal("no mutation found")
			}
			wantOriginal := tc.snippet
			if wantOriginal == "" {
				wantOriginal = string(source)
			}
			wantMutated := strings.Replace(wantOriginal, tc.original, tc.mutated, 1)
			if delta := cmp.Diff(wantOriginal, string(found.OrigSnippet())); delta != "" {
				t.Fatal(delta)
			}
			if delta := cmp.Diff(wantMutated, string(found.MutatedSnippet())); delta != "" {
				t.Fatal(delta)
			}
			if found.Status() != mutator.NotCovered {
				t.Fatalf("status=%s", found.Status())
			}
			if delta := cmp.Diff(string(source), string(fs["source.go"].Data)); delta != "" {
				t.Fatalf("discovery changed source: %s", delta)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "source.go")
			if err := os.WriteFile(path, source, 0o600); err != nil {
				t.Fatal(err)
			}
			found.SetWorkdir(dir)
			if err := found.Apply(); err != nil {
				t.Fatal(err)
			}
			if err := found.Rollback(); err != nil {
				t.Fatal(err)
			}
			if string(found.MutatedSnippet()) != wantMutated {
				t.Fatal("preview changed after execution")
			}
			//nolint:gosec // path is a test-owned file in t.TempDir
			got, err := os.ReadFile(path)
			if err != nil || !cmp.Equal(source, got) {
				t.Fatalf("source not restored: %s, %v", got, err)
			}
		})
	}
}

func TestDiscoveryIncludesUncoveredContext(t *testing.T) {
	viperSet(map[string]any{configuration.RunDryRunKey: true})
	defer viperReset()
	source := []byte("package example\nfunc f(a,b int)int{return a+b}\n")
	mod := gomodule.GoModule{Name: "example.com", Root: ".", CallingDir: "."}
	mu := engine.New(mod, engine.CodeData{}, newJobDealerStub(t), engine.WithDirFs(fstest.MapFS{"file.go": {Data: source}}))
	result := runEngine(t, &mu)
	if len(result.Mutants) != 1 {
		t.Fatalf("expected one mutant, got %d", len(result.Mutants))
	}
	m := result.Mutants[0]
	if m.Status() != mutator.NotCovered || !strings.Contains(string(m.MutatedSnippet()), "a-b") {
		t.Fatalf("uncovered context missing: status %s, snippet %q", m.Status(), m.MutatedSnippet())
	}
}
