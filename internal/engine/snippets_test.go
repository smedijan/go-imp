package engine

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestSnippetPreviews(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   string
		original string
		mutated  string
		kind     mutator.Type
	}{
		{name: "boundary with longer token", source: "func f(a,b int)bool{return a>b}", original: "a>b", mutated: "a>=b", kind: mutator.ConditionalsBoundary},
		{name: "boundary with shorter token", source: "func f(a,b int)bool{return a>=b}", original: "a>=b", mutated: "a>b", kind: mutator.ConditionalsBoundary},
		{name: "unary", source: "func f(a int)int{return -a}", original: "-a", mutated: "+a", kind: mutator.InvertNegatives},
		{name: "assignment", source: "func f(a int){a+=2}", original: "a+=2", mutated: "a=2", kind: mutator.RemoveSelfAssignments},
		{name: "increment", source: "func f(a int){a++}", original: "a++", mutated: "a--", kind: mutator.IncrementDecrement},
		{name: "branch", source: "func f(){for{break}}", original: "break", mutated: "continue", kind: mutator.InvertLoopCtrl},
		{name: "multiline and Unicode", source: "// café\nfunc f(a,b int) bool {\n\n\n\nreturn a>b\n}", original: "a>b", mutated: "a>=b", kind: mutator.ConditionalsBoundary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte("package example\n" + tc.source + "\n")
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, "source.go", source, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			var tm *TokenMutator
			ast.Inspect(file, func(n ast.Node) bool {
				node, ok := NewTokenNode(n)
				if !ok || tm != nil {
					return true
				}
				if _, ok := tokenMutations[tc.kind][node.Tok()]; ok {
					tm = NewTokenMutant("example.com", set, file, node)
					tm.SetType(tc.kind)
				}

				return true
			})
			if tm == nil {
				t.Fatal("no mutation found")
			}
			originalToken := tm.tokenNode.Tok()
			tm.captureSnippets(source)
			if tm.tokenNode.Tok() != originalToken {
				t.Fatal("preview changed AST")
			}
			wantOriginal := extractSnippet(source, tm.Position().Line, 3)
			wantMutated := strings.Replace(string(wantOriginal), tc.original, tc.mutated, 1)
			if diff := cmp.Diff(string(wantOriginal), string(tm.OrigSnippet())); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(wantMutated, string(tm.MutatedSnippet())); diff != "" {
				t.Fatal(diff)
			}
			// Applying and rolling back must preserve the correctly aligned preview,
			// even when go/printer reformats the actual mutated file.
			dir := t.TempDir()
			path := filepath.Join(dir, "source.go")
			if err := os.WriteFile(path, source, 0o600); err != nil {
				t.Fatal(err)
			}
			tm.SetWorkdir(dir)
			if err := tm.Apply(); err != nil {
				t.Fatal(err)
			}
			if err := tm.Rollback(); err != nil {
				t.Fatal(err)
			}
			if string(tm.MutatedSnippet()) != wantMutated {
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
	key := configuration.MutantTypeEnabledKey(mutator.ArithmeticBase)
	previous := configuration.Get[any](key)
	t.Cleanup(func() { configuration.Set(key, previous) })
	configuration.Set(key, true)
	source := []byte("package example\nfunc f(a,b int)int{return a+b}\n")
	mu := Engine{fs: fstest.MapFS{"file.go": {Data: source}}, mutantStream: make(chan mutator.Mutator, 1)}
	mu.runOnFile("file.go")
	if len(mu.mutantStream) != 1 {
		t.Fatal("expected one mutant")
	}
	m := <-mu.mutantStream
	if m.Status() != mutator.NotCovered || !strings.Contains(string(m.MutatedSnippet()), "a-b") {
		t.Fatalf("uncovered context missing: status %s, snippet %q", m.Status(), m.MutatedSnippet())
	}
}

func TestRecordedTestCommand(t *testing.T) {
	for _, integration := range []bool{false, true} {
		t.Run(map[bool]string{false: "package", true: "integration"}[integration], func(t *testing.T) {
			dir := t.TempDir()
			tm := &TokenMutator{workDir: dir}
			m := mutantExecutor{
				mutant: tm, wdDealer: workdir.NewCachedDealer(dir, dir),
				buildTags: "tag1,tag2", testCPU: 2, testExecutionTime: 5 * time.Second, integrationMode: integration,
				execContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
					if name != "go" {
						t.Errorf("unexpected executable: %s", name)
					}
					if diff := cmp.Diff(recordedTestArgs(integration), args); diff != "" {
						t.Error(diff)
					}

					return exec.CommandContext(ctx, "go", "version")
				},
			}
			if status := m.runTests(dir, "example.com/pkg"); status != mutator.Lived {
				t.Fatalf("status = %s", status)
			}
			want := append([]string{"go"}, recordedTestArgs(integration)...)
			if diff := cmp.Diff(want, tm.TestCommand()); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func recordedTestArgs(integration bool) []string {
	pkg := "example.com/pkg"
	if integration {
		pkg = "./..."
	}

	return []string{"test", "-tags", "tag1,tag2", "-timeout", "7s", "-failfast", "-cpu", "2", pkg}
}
