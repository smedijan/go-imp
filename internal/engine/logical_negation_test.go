package engine //nolint:testpackage // Verify discovery-time snippet capture alongside application.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/internal/mutator"
)

func TestLogicalNegation(t *testing.T) {
	t.Parallel()
	for _, expr := range []string{"!a", "!(a && b)", "! /* comment */ a", "!!a", "!(!a || !b)", "! // comment\n a"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			source := "package example\nfunc f(a, b bool) bool { return " + expr + " }\n"
			fs := token.NewFileSet()
			file, err := parser.ParseFile(fs, "source.go", source, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			var nodes []*ast.UnaryExpr
			ast.Inspect(file, func(n ast.Node) bool {
				if u, ok := n.(*ast.UnaryExpr); ok && u.Op == token.NOT {
					nodes = append(nodes, u)
				}

				return true
			})
			for _, u := range nodes {
				node, ok := NewTokenNode(u)
				if !ok {
					t.Fatal("unary node not supported")
				}
				if got := TokenMutantType[token.NOT]; len(got) != 1 || got[0] != mutator.InvertLogical {
					t.Fatalf("unexpected mapping: %v", got)
				}
				m := NewTokenMutant("example", fs, file, node)
				m.SetType(mutator.InvertLogical)
				m.SetWorkdir(t.TempDir())
				filename := filepath.Join(m.Workdir(), "source.go")
				if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				pos := m.Position()
				want := source[:pos.Offset] + "!" + source[pos.Offset:]
				m.captureSnippets([]byte(source))
				if string(m.MutatedSnippet()) != string(extractSnippet([]byte(want), pos.Line, 3)) {
					t.Fatal("incorrect preview")
				}
				// Repeated application after rollback must leave the shared AST untouched.
				for range 2 {
					if err := m.Apply(); err != nil {
						t.Fatal(err)
					}
					// #nosec G304 -- filename is inside t.TempDir.
					got, err := os.ReadFile(filename)
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != want {
						t.Fatalf("got %q, want %q", got, want)
					}
					mutatedFS := token.NewFileSet()
					mutated, err := parser.ParseFile(mutatedFS, "source.go", got, parser.ParseComments)
					if err != nil {
						t.Fatal(err)
					}
					conf := types.Config{}
					if _, err := conf.Check("example", mutatedFS, []*ast.File{mutated}, nil); err != nil {
						t.Fatal(err)
					}
					if m.Position() != pos || u.Op != token.NOT {
						t.Fatal("shared AST changed")
					}
					if err := m.Rollback(); err != nil {
						t.Fatal(err)
					}
					// #nosec G304 -- filename is inside t.TempDir.
					restored, err := os.ReadFile(filename)
					if err != nil {
						t.Fatal(err)
					}
					if string(restored) != source {
						t.Fatal("rollback changed source")
					}
				}
			}
		})
	}
}
