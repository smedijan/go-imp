package coverage_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/gomodule"
)

func TestTaglessSwitchCaseCoverage(t *testing.T) {
	const filename = "testdata/tagless-switch/switch.txt"
	source, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var switchConditions, ifConditions, fallthroughConditions []token.Position
	ast.Inspect(file, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		position := fset.Position(binary.OpPos)
		position.Filename = "switch.go"
		switch {
		case position.Line < 15:
			switchConditions = append(switchConditions, position)
		case position.Line < 24:
			ifConditions = append(ifConditions, position)
		default:
			fallthroughConditions = append(fallthroughConditions, position)
		}

		return true
	})

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(workingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()
	root := t.TempDir()
	//nolint:gosec // root is a test-owned temporary directory.
	if err := os.WriteFile(filepath.Join(root, "switch.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	profileData, err := os.ReadFile("testdata/tagless-switch/coverage")
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // root is a test-owned temporary directory.
	if err := os.WriteFile(filepath.Join(root, "coverage"), profileData, 0o600); err != nil {
		t.Fatal(err)
	}
	mod := gomodule.GoModule{
		Name:       "example.com",
		Root:       root,
		CallingDir: ".",
	}
	result, err := coverage.NewWithCmd(fakeExecCommandSuccess(nil), root, mod).Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(switchConditions) != 4 || len(ifConditions) != 2 || len(fallthroughConditions) != 2 {
		t.Fatalf("unexpected fixture conditions: switch=%d if=%d fallthrough=%d", len(switchConditions), len(ifConditions), len(fallthroughConditions))
	}
	for i, position := range switchConditions {
		want := i == 0 || i == 3 // Later alternative and uncovered second case did not run.
		if got := result.Profile.IsCovered(position); got != want {
			t.Errorf("switch condition at %s covered=%t, want %t", position, got, want)
		}
	}
	for _, position := range ifConditions {
		if !result.Profile.IsCovered(position) {
			t.Errorf("if condition at %s should be covered", position)
		}
	}
	if !result.Profile.IsCovered(fallthroughConditions[0]) {
		t.Errorf("fallthrough source condition at %s should be covered", fallthroughConditions[0])
	}
	if result.Profile.IsCovered(fallthroughConditions[1]) {
		t.Errorf("fallthrough destination condition at %s should be uncovered", fallthroughConditions[1])
	}
}
