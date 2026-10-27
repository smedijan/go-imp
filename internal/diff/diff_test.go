package diff_test

import (
	"go/token"
	"testing"

	"github.com/smedijan/go-imp/internal/diff"
)

func TestDiffIsChanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		diff diff.Diff
		pos  token.Position
		want bool
	}{
		{name: "nil diff", want: true},
		{name: "empty diff", diff: diff.Diff{}, want: true},
		{name: "in range", diff: diff.Diff{"test": {{StartLine: 21, EndLine: 21}}}, pos: token.Position{Filename: "test", Line: 21}, want: true},
		{name: "outside range", diff: diff.Diff{"test": {{StartLine: 21, EndLine: 21}}}, pos: token.Position{Filename: "test", Line: 22}},
		{name: "other file", diff: diff.Diff{"test": {{StartLine: 21, EndLine: 21}}}, pos: token.Position{Filename: "other", Line: 21}},
		{name: "windows path key", diff: diff.Diff{`pkg\changed.go`: {{StartLine: 3, EndLine: 3}}}, pos: token.Position{Filename: "pkg/changed.go", Line: 3}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.diff.IsChanged(tc.pos); got != tc.want {
				t.Errorf("IsChanged()=%t, want %t", got, tc.want)
			}
		})
	}
}
