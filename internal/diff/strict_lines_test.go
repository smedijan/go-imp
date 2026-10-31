package diff_test

import (
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/diff"
)

func TestSelectionStrictChangedLineVersusImpact(t *testing.T) {
	for _, tc := range []struct {
		name           string
		edit           func(*testing.T, string, func(string, string))
		strict, impact []token.Position
	}{
		{name: "production and test mixed", edit: func(_ *testing.T, _ string, write func(string, string)) {
			write("a/code.go", "package a\nfunc First(a,b int) int {\n _ = a + 0\n return a-b\n}\nfunc Second(a,b int) int {return a-b}\n")
			write("a/code_test.go", "package a\n// improved test\n")
		}, strict: []token.Position{{Filename: "a/code.go", Line: 4}}, impact: []token.Position{{Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "a/code.go", Line: 6}, {Filename: "b/code.go", Line: 3}}},
		{name: "production reverse dependency", edit: func(_ *testing.T, _ string, write func(string, string)) {
			write("a/code.go", "package a\nfunc First(a,b int) int {\n _ = a + 0\n return a-b\n}\nfunc Second(a,b int) int {return a-b}\n")
		}, strict: []token.Position{{Filename: "a/code.go", Line: 4}}, impact: []token.Position{{Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "b/code.go", Line: 3}}},
		{name: "test only", edit: func(_ *testing.T, _ string, write func(string, string)) {
			write("a/code_test.go", "package a\n// improved test\n")
		}, impact: []token.Position{{Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "a/code.go", Line: 6}, {Filename: "b/code.go", Line: 3}}},
		{name: "deletion only", edit: func(_ *testing.T, _ string, write func(string, string)) {
			write("a/code.go", "package a\nfunc First(a,b int) int {\n return a+b\n}\nfunc Second(a,b int) int {return a-b}\n")
		}, impact: []token.Position{{Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "a/code.go", Line: 6}, {Filename: "b/code.go", Line: 3}}},
		{name: "deleted production file", edit: func(t *testing.T, root string, _ func(string, string)) {
			if err := os.Remove(filepath.Join(root, "a", "deleted.go")); err != nil {
				t.Fatal(err)
			}
		}, impact: []token.Position{{Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "a/code.go", Line: 6}, {Filename: "b/code.go", Line: 3}}},
		{name: "rename only", edit: func(t *testing.T, root string, _ func(string, string)) {
			runGit(t, root, "mv", "a/code.go", "a/renamed.go")
		}, impact: []token.Position{{Filename: "a/renamed.go", Line: 4}, {Filename: "b/code.go", Line: 3}}},
		{name: "new untracked production", edit: func(_ *testing.T, _ string, write func(string, string)) {
			write("a/new.go", "package a\nfunc New(a,b int) int {return a+b}\n")
		}, strict: []token.Position{{Filename: "a/new.go", Line: 2}}, impact: []token.Position{{Filename: "a/new.go", Line: 2}, {Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "a/code.go", Line: 6}, {Filename: "b/code.go", Line: 3}}},
		{name: "new tracked production", edit: func(t *testing.T, root string, write func(string, string)) {
			write("a/new.go", "package a\nfunc New(a,b int) int {return a+b}\n")
			runGit(t, root, "add", "a/new.go")
		}, strict: []token.Position{{Filename: "a/new.go", Line: 2}}, impact: []token.Position{{Filename: "a/new.go", Line: 2}, {Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "a/code.go", Line: 6}, {Filename: "b/code.go", Line: 3}}},
		{name: "configuration only", edit: func(_ *testing.T, _ string, write func(string, string)) { write(".imp.yaml", "{}\n") }, impact: []token.Position{{Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "a/code.go", Line: 6}, {Filename: "b/code.go", Line: 3}, {Filename: "unrelated/code.go", Line: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod, write := selectionReviewFixture(t, map[string]string{
				"a/code.go":         "package a\nfunc First(a,b int) int {\n _ = a + 0\n return a+b\n}\nfunc Second(a,b int) int {return a-b}\n",
				"a/code_test.go":    "package a\n",
				"a/deleted.go":      "package a\nfunc Removed(a,b int) int {return a+b}\n",
				"b/code.go":         "package b\nimport _ \"example.com/impact/a\"\nfunc Value(a,b int) int {return a+b}\n",
				"unrelated/code.go": "package unrelated\nfunc Value(a,b int) int {return a+b}\n",
			})
			tc.edit(t, mod.Root, write)
			for _, scope := range []string{diff.ChangedLine, diff.ChangedFunction, diff.AffectedPackage} {
				configuration.Set(configuration.RunDiffScope, scope)
				selected, info, err := diff.NewSelection(mod)
				if err != nil {
					t.Fatal(err)
				}
				wanted := tc.impact
				if scope == diff.AffectedPackage {
					wanted = append(append([]token.Position{}, wanted...), token.Position{Filename: "a/code.go", Line: 6})
				}
				if scope == diff.ChangedLine {
					wanted = tc.strict
				}
				for _, pos := range []token.Position{{Filename: "a/code.go", Line: 3}, {Filename: "a/code.go", Line: 4}, {Filename: "a/code.go", Line: 6}, {Filename: "a/renamed.go", Line: 4}, {Filename: "a/new.go", Line: 2}, {Filename: "b/code.go", Line: 3}, {Filename: "unrelated/code.go", Line: 2}} {
					want := false
					for _, candidate := range wanted {
						if candidate == pos {
							want = true
						}
					}
					// Deleted/renamed old paths are never production candidates.
					if _, err := os.Stat(filepath.Join(mod.Root, pos.Filename)); os.IsNotExist(err) {
						want = false
					}
					if got := selected.IsChanged(pos); got != want {
						t.Errorf("%s selected %+v=%t want %t: %v %+v", scope, pos, got, want, selected, info)
					}
				}
				if scope == diff.ChangedLine {
					effective := diff.ChangedLine
					if len(tc.strict) == 0 {
						effective = "empty"
					}
					if info.Requested != diff.ChangedLine || info.Effective != effective {
						t.Errorf("strict scope broadened: %+v", info)
					}
					if len(tc.strict) == 0 && !strings.Contains(strings.Join(info.Reasons, ";"), "--diff-scope changed-function") {
						t.Errorf("missing impact suggestion: %+v", info)
					}
				}
			}
		})
	}
}
