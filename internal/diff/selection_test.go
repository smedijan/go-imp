package diff_test

import (
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/gomodule"
)

func TestSelectionPolicies(t *testing.T) {
	withoutGitEnvironment(t)
	for _, tc := range []struct {
		name, scope        string
		edits              map[string]string
		rename             bool
		selected, rejected []token.Position
		effective, reason  string
	}{
		{name: "line", scope: diff.ChangedLine, edits: map[string]string{"a/a.go": "package a\nfunc First(a int) int {\n return a - 1\n}\nfunc Second(a int) int {\n return a - 1\n}\n"}, selected: []token.Position{{Filename: "a/a.go", Line: 3}, {Filename: "b/b.go", Line: 3}, {Filename: "c/c.go", Line: 3}}, rejected: []token.Position{{Filename: "a/a.go", Line: 2}, {Filename: "a/helper.go", Line: 3}}, effective: diff.AffectedPackage, reason: "reverse dependency"},
		{name: "changed reverse dependent", scope: diff.ChangedLine, edits: map[string]string{"a/helper.go": "package a\nfunc Helper(a int) int { return a - 1 }\n", "b/b.go": "package b\nimport \"example/a\"\nfunc Value(x int) int {return a.First(x)+1}\n// edited too\n"}, selected: []token.Position{{Filename: "b/b.go", Line: 3}}, reason: "reverse dependency"},
		{name: "function", scope: diff.ChangedFunction, edits: map[string]string{"a/a.go": "package a\nfunc First(a int) int {\n return a - 1\n}\nfunc Second(a int) int {\n return a - 1\n}\n"}, selected: []token.Position{{Filename: "a/a.go", Line: 2}, {Filename: "a/a.go", Line: 4}}, rejected: []token.Position{{Filename: "a/a.go", Line: 6}}},
		{name: "package", scope: diff.AffectedPackage, edits: map[string]string{"a/helper.go": "package a\nfunc Helper(a int) int { return a - 1 }\n"}, selected: []token.Position{{Filename: "a/a.go", Line: 6}}},
		{name: "test", scope: diff.ChangedLine, edits: map[string]string{"a/a_test.go": "package a\n// test improvement\n"}, selected: []token.Position{{Filename: "a/helper.go", Line: 99}}, reason: "test change"},
		{name: "deletion", scope: diff.ChangedLine, edits: map[string]string{"a/a.go": "package a\nfunc First(a int) int {\n return a + 1\n}\n"}, selected: []token.Position{{Filename: "a/helper.go", Line: 99}}, reason: "deletion fallback"},
		{name: "deleted file", scope: diff.ChangedLine, edits: map[string]string{"a/helper.go": ""}, selected: []token.Position{{Filename: "b/b.go", Line: 99}, {Filename: "a/a.go", Line: 3}}, reason: "deleted source"},
		{name: "rename", scope: diff.ChangedLine, rename: true, selected: []token.Position{{Filename: "a/a.go", Line: 99}, {Filename: "b/b.go", Line: 99}}, reason: "renamed source"},
		{name: "untracked", scope: diff.ChangedFunction, edits: map[string]string{"a/new.go": "package a\n"}, selected: []token.Position{{Filename: "a/helper.go", Line: 99}}, reason: "new/untracked"},
		{name: "configuration", scope: diff.ChangedLine, edits: map[string]string{"settings.json": "{}\n"}, selected: []token.Position{{Filename: "unrelated/u.go", Line: 99}}, effective: "module", reason: "configuration"},
		{name: "empty", scope: diff.ChangedLine, rejected: []token.Position{{Filename: "a/a.go", Line: 3}}, effective: "empty"},
		{name: "top-level", scope: diff.ChangedFunction, edits: map[string]string{"a/helper.go": "package a\nvar Global = 1\nfunc Helper(a int) int { return a + 1 }\n"}, selected: []token.Position{{Filename: "a/a.go", Line: 99}}, reason: "outside function"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			root := t.TempDir()
			files := map[string]string{
				"go.mod":         "module example\n\ngo 1.25.0\n",
				"a/a.go":         "package a\nfunc First(a int) int {\n return a + 1\n}\nfunc Second(a int) int {\n return a - 1\n}\n",
				"a/helper.go":    "package a\nfunc Helper(a int) int { return a + 1 }\n",
				"a/a_test.go":    "package a_test\nimport _ \"example/a\"\n",
				"b/b.go":         "package b\nimport \"example/a\"\nfunc Value(x int) int {return a.First(x)+1}\n",
				"c/c.go":         "package c\nfunc Value(x int) int {return x+1}\n",
				"c/c_test.go":    "package c\nimport _ \"example/b\"\n",
				"unrelated/u.go": "package unrelated\nfunc Value(x int) int {return x+1}\n",
			}
			write := func(name, source string) {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for name, source := range files {
				write(name, source)
			}
			runGit(t, root, "init")
			runGit(t, root, "add", ".")
			runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
			for name, source := range tc.edits {
				if source == "" {
					if err := os.Remove(filepath.Join(root, name)); err != nil {
						t.Fatal(err)
					}
				} else {
					write(name, source)
				}
			}
			if tc.rename {
				runGit(t, root, "mv", "a/helper.go", "b/helper.go")
				write("b/helper.go", "package b\nfunc Helper(a int) int { return a + 1 }\n")
			}
			configuration.Set(configuration.RunDiffRef, "HEAD")
			configuration.Set(configuration.RunDiffScope, tc.scope)
			selected, info, err := diff.NewSelection(gomodule.GoModule{Root: root, Name: "example", CallingDir: "."})
			if err != nil {
				t.Fatal(err)
			}
			for _, pos := range tc.selected {
				if !selected.IsChanged(pos) {
					t.Errorf("not selected: %+v (%v)", pos, selected)
				}
			}
			for _, pos := range append(tc.rejected, token.Position{Filename: "absent.go", Line: 1}) {
				if selected.IsChanged(pos) {
					t.Errorf("unexpectedly selected: %+v", pos)
				}
			}
			if tc.effective != "" && info.Effective != tc.effective {
				t.Errorf("effective=%s want %s", info.Effective, tc.effective)
			}
			if tc.reason != "" && !strings.Contains(strings.Join(info.Reasons, ";"), tc.reason) {
				t.Errorf("missing reason %q: %v", tc.reason, info.Reasons)
			}
		})
	}
}

func TestValidateScope(t *testing.T) {
	for _, scope := range []string{"", diff.ChangedLine, diff.ChangedFunction, diff.AffectedPackage} {
		if err := diff.ValidateScope(scope); err != nil {
			t.Error(err)
		}
	}
	if diff.ValidateScope("unknown") == nil {
		t.Error("invalid policy accepted")
	}
}
