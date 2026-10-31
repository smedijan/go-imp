package engine_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestEngineZeroDivisionApplicability(t *testing.T) {
	viperSet(map[string]any{configuration.RunDryRunKey: true})
	defer viperReset()
	for _, tc := range []struct {
		name     string
		function string
		retained bool
	}{
		{"integer", "func f(x int) { _ = x * 0 }", false},
		{"float", "func f(x float64) { _ = x * 0 }", true},
		{"complex", "func f(x complex128) { _ = x * 0 }", true},
		{"named integer", "func f(x Integer) { _ = x * 0 }", false},
		{"named float", "func f(x Float) { _ = x * 0 }", true},
		{"named complex", "func f(x Complex) { _ = x * 0 }", true},
		{"integer constant", "func f() { _ = 1 * 0 }", false},
		{"float constant", "func f() { _ = 1.5 * 0 }", false},
		{"complex constant", "func f() { _ = (1+2i) * 0 }", false},
		{"named float constant", "func f() { const x Float = 1; _ = x * 0 }", false},
		{"compound integer", "func f(x int) { x *= 0 }", false},
		{"compound float", "func f(x float64) { x *= 0 }", true},
		{"compound complex", "func f(x complex128) { x *= 0 }", true},
		{"compound named integer", "func f(x Integer) { x *= 0 }", false},
		{"compound named float", "func f(x Float) { x *= 0 }", true},
		{"compound named complex", "func f(x Complex) { x *= 0 }", true},
		{"generic integer", "func f[T ~int](x T) { _ = x * 0 }", false},
		{"generic integer union", "func f[T ~int | ~uint](x T) { _ = x * 0 }", false},
		{"generic float", "func f[T ~float64](x T) { _ = x * 0 }", true},
		{"generic complex", "func f[T ~complex128](x T) { _ = x * 0 }", true},
		{"generic mixed float", "func f[T ~int | ~float64](x T) { _ = x * 0 }", true},
		{"generic mixed complex", "func f[T ~int | ~complex128](x T) { _ = x * 0 }", true},
		{"generic intersection", "func f[T interface { ~int | ~float64; ~int }](x T) { _ = x * 0 }", false},
		{"generic embedded integer", "func f[T Integers](x T) { _ = x * 0 }", false},
		{"compound generic integer", "func f[T ~int](x T) { x *= 0 }", false},
		{"compound generic float", "func f[T ~float64](x T) { x *= 0 }", true},
		{"compound generic complex", "func f[T ~complex128](x T) { x *= 0 }", true},
		{"compound generic mixed", "func f[T ~int | ~float64](x T) { x *= 0 }", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "package sample\ntype Integer int\ntype Float float64\ntype Complex complex128\ntype Integers interface { ~int | ~uint }\n" + tc.function + "\n"
			// Check the replacement itself too: the conservative filter must agree
			// with Go on these integer, constant, floating, complex and generic cases.
			replacement := strings.ReplaceAll(strings.ReplaceAll(source, "* 0", "/ 0"), "*= 0", "/= 0")
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, "source.go", replacement, 0)
			if err != nil {
				t.Fatal(err)
			}
			config := types.Config{}
			_, typeErr := config.Check("sample", set, []*ast.File{file}, nil)
			if (typeErr == nil) != tc.retained {
				t.Fatalf("replacement validity differs from test expectation: %v", typeErr)
			}
			root := writePackageFiles(t, map[string]string{"source.go": source})
			e := engine.New(gomodule.GoModule{Name: "example.com/discovery", Root: root, CallingDir: "."}, engine.CodeData{}, newJobDealerStub(t))
			results, err := e.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			offset := strings.Index(source, "* 0")
			kind := mutator.ArithmeticBase
			if strings.Contains(tc.function, "*= 0") {
				kind = mutator.InvertAssignments
				offset = strings.Index(source, "*= 0")
			}
			found := false
			for _, m := range results.Mutants {
				if m.Type() == kind && m.Position().Offset == offset {
					found = true
				}
			}
			if found != tc.retained {
				t.Fatalf("replacement candidate retained=%t, want %t", found, tc.retained)
			}
		})
	}
}
