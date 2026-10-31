package engine //nolint:testpackage // Verify identity boundary and normalization contracts directly.

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestNormalizedOriginalSourceTokens(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"b&& /* comment */ c", "b && c"},
		{"call(\"a b\", `raw\ntext`)", "call ( \"a b\" , `raw\ntext` )"},
		{"x", "x"},
		{"x;", "x ;"},
		{"a\n+b", "a \n + b"},
		{"", ""},
	} {
		t.Run(tc.source, func(t *testing.T) {
			if got := normalizedOriginal(tc.source); got != tc.want {
				t.Fatalf("normalizedOriginal(%q)=%q, want %q", tc.source, got, tc.want)
			}
		})
	}
}

func TestSourceIdentityEnclosingDeclarationBoundaries(t *testing.T) {
	source := []byte("package p\nvar n=1\nfunc f(a int)int{return a+1}\n")
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "source.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		start, end := set.PositionFor(decl.Pos(), false).Offset, set.PositionFor(decl.End(), false).Offset
		for _, tc := range []struct {
			offset  int
			present bool
		}{{start, true}, {end - 1, true}, {end, false}, {start - 1, false}} {
			got := sourceIdentity("module", "source.go", set, file, tc.offset, "x", "y", source)
			if (got.ID != "") != tc.present {
				t.Fatalf("offset=%d declaration=[%d,%d), identity=%+v", tc.offset, start, end, got)
			}
		}
	}
}

func TestSourceIdentityRejectsNonTokenOffset(t *testing.T) {
	source := []byte("package p\nfunc f(a int)int{return a+1}\n")
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "source.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(string(source), "return") + 1
	if got := sourceIdentity("module", "source.go", set, file, offset, "x", "y", source); got.ID != "" {
		t.Fatalf("non-token offset received identity: %+v", got)
	}
}
