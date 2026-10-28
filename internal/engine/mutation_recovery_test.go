package engine_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/mutator"
)

func arithmeticMutant(t *testing.T, dir string) (*engine.TokenMutator, *ast.BinaryExpr) {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "source.go", "package example\nfunc Value() int { return 1+2 }\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	var expression *ast.BinaryExpr
	ast.Inspect(file, func(node ast.Node) bool {
		if binary, ok := node.(*ast.BinaryExpr); ok {
			expression = binary
		}

		return true
	})
	node, ok := engine.NewTokenNode(expression)
	if !ok {
		t.Fatal("missing arithmetic token")
	}
	m := engine.NewTokenMutant("example.com", set, file, node)
	m.SetType(mutator.ArithmeticBase)
	m.SetWorkdir(dir)

	return m, expression
}

func TestRollbackAfterFailedReadDoesNotCreateSource(t *testing.T) {
	dir := t.TempDir()
	m, _ := arithmeticMutant(t, dir)
	if err := m.Apply(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing source, got %v", err)
	}
	if err := m.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "source.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Rollback created missing source: %v", err)
	}
}

func TestFailedMutationWriteRestoresSharedAST(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.go")
	original := []byte("package example\nfunc Value() int { return 1+2 }\n")
	if err := os.WriteFile(path, original, 0o400); err != nil {
		t.Fatal(err)
	}
	m, expression := arithmeticMutant(t, dir)
	applyErr := m.Apply()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(); err != nil {
		t.Fatal(err)
	}
	if applyErr == nil {
		t.Skip("filesystem permits writes to a read-only file")
	}
	if expression.Op != token.ADD {
		t.Fatalf("failed write corrupted shared AST: operator=%s", expression.Op)
	}
	//nolint:gosec // test-owned source file
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("original source not restored: %q, %v", got, err)
	}
}
