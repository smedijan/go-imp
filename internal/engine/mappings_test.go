package engine //nolint:testpackage // Inspect token substitutions independently of formatting.

import (
	"go/ast"
	"go/token"
	"testing"

	"github.com/smedijan/go-imp/internal/mutator"
)

func TestBooleanReplacementText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ original, replacement string }{
		{"true", "false"},
		{"false", "true"},
	} {
		m := NewTokenMutant("example", token.NewFileSet(), nil, NewBooleanNode(&ast.Ident{Name: tc.original}))
		m.SetType(mutator.BooleanLiteral)
		original, replacement, ok := m.replacementText()
		if !ok || original != tc.original || replacement != tc.replacement {
			t.Fatalf("replacementText(%s) = %q, %q, %t", tc.original, original, replacement, ok)
		}
	}
}

func TestTokenMappingsRejectIdentity(t *testing.T) {
	t.Parallel()
	for family, mappings := range tokenMutations {
		// Boolean literals change identifier text, not the IDENT token kind.
		if family == mutator.BooleanLiteral {
			continue
		}
		for original, replacement := range mappings {
			if original == replacement {
				t.Errorf("%s maps %s to itself", family, original)
			}
		}
	}
	for original, families := range TokenMutantType {
		for _, family := range families {
			if family == mutator.InvertLogical && original == token.NOT {
				continue
			}
			replacement, ok := tokenMutations[family][original]
			if !ok || original == replacement {
				t.Errorf("invalid discovery mapping: %s %s -> %s", family, original, replacement)
			}
		}
	}
}
