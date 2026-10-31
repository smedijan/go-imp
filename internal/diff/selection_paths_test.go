package diff_test

import (
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/gomodule"
)

func TestSelectionPathsAndBuildSettings(t *testing.T) {
	withoutGitEnvironment(t)
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	repo := t.TempDir()
	root := filepath.Join(repo, "nested")
	write := func(name, source string) {
		t.Helper()
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("outside.go", "package outside\n")
	write("nested/go.mod", "module example/nested\n\ngo 1.25.0\n")
	write("nested/a/a.go", "package a\nfunc Value(a,b int) int {return a+b}\n")
	write("nested/a/active.go", "//go:build impact_special\n\npackage a\nfunc Tagged(a,b int) int {return a-b}\n")
	write("nested/b/b.go", "package b\nimport _ \"example/nested/a\"\nfunc Value(a,b int) int {return a+b}\n")
	runGit(t, repo, "init")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	configuration.Set(configuration.RunDiffRef, "HEAD")
	configuration.Set(configuration.RunDiffScope, diff.AffectedPackage)
	mod := gomodule.GoModule{Root: root, Name: "example/nested", CallingDir: "."}
	write("outside.go", "package outside\n// unrelated\n")
	selected, info, err := diff.NewSelection(mod)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 || info.Effective != "empty" || !strings.Contains(strings.Join(info.Reasons, ";"), "ignored out-of-module path: outside.go") {
		t.Fatalf("out-of-module selection: %v %+v", selected, info)
	}
	write("nested/a/extra_test.go", "package a\n")
	selected, _, err = diff.NewSelection(mod)
	if err != nil {
		t.Fatal(err)
	}
	if !selected.IsChanged(token.Position{Filename: "b/b.go", Line: 3}) || selected.IsChanged(token.Position{Filename: "a/active.go", Line: 4}) {
		t.Fatalf("build/default selection: %v", selected)
	}
	configuration.Set(configuration.RunTagsKey, "impact_special")
	mod.CallingDir = "a"
	selected, _, err = diff.NewSelection(mod)
	if err != nil {
		t.Fatal(err)
	}
	if !selected.IsChanged(token.Position{Filename: "active.go", Line: 4}) || selected.IsChanged(token.Position{Filename: "../b/b.go", Line: 3}) {
		t.Fatalf("tagged/calling-directory selection: %v", selected)
	}
	// A repository workspace configuration can influence a nested module, unlike arbitrary outside source.
	write("go.work", "go 1.25.0\nuse ./nested\n")
	selected, info, err = diff.NewSelection(mod)
	if err != nil {
		t.Fatal(err)
	}
	if info.Effective != "module" || !selected.IsChanged(token.Position{Filename: "a.go", Line: 2}) {
		t.Fatalf("workspace fallback: %v %+v", selected, info)
	}
}

func TestSelectionModuleRootSymlinkAlias(t *testing.T) {
	for _, scope := range []string{diff.ChangedLine, diff.ChangedFunction, diff.AffectedPackage} {
		t.Run(scope, func(t *testing.T) {
			mod, write := selectionReviewFixture(t, map[string]string{
				"a/code.go":     "package a\nfunc Value(a,b int) int {return a+b}\n",
				"a/deleted.go":  "package a\nfunc Removed(a,b int) int {return a-b}\n",
				"b/code.go":     "package b\nimport _ \"example.com/impact/a\"\nfunc Value(a,b int) int {return a+b}\n",
				"other/code.go": "package other\nfunc Value(a,b int) int {return a+b}\n",
			})
			alias := filepath.Join(t.TempDir(), "module-alias")
			if err := os.Symlink(mod.Root, alias); err != nil {
				t.Skipf("directory symlink unavailable: %v", err)
			}
			write("a/code.go", "package a\nfunc Value(a,b int) int {return a-b}\n")
			configuration.Set(configuration.RunDiffScope, scope)
			direct, _, err := diff.NewSelection(mod)
			if err != nil {
				t.Fatal(err)
			}
			mod.Root = alias
			selected, info, err := diff.NewSelection(mod)
			if err != nil {
				t.Fatal(err)
			}
			for _, pos := range []token.Position{{Filename: "a/code.go", Line: 2}, {Filename: "b/code.go", Line: 3}} {
				if !selected.IsChanged(pos) {
					t.Errorf("symlink alias lost selected source %+v: %v %+v", pos, selected, info)
				}
			}
			if selected.IsChanged(token.Position{Filename: "other/code.go", Line: 2}) {
				t.Error("unrelated package selected")
			}
			if !reflect.DeepEqual(selected, direct) {
				t.Errorf("alias scope differs: %v, direct %v", selected, direct)
			}
			// Deleted paths must be mapped lexically below the canonical existing root.
			if err := os.Remove(filepath.Join(alias, "a", "deleted.go")); err != nil {
				t.Fatal(err)
			}
			mod.CallingDir = "a"
			selected, info, err = diff.NewSelection(mod)
			if err != nil {
				t.Fatal(err)
			}
			if !selected.IsChanged(token.Position{Filename: "code.go", Line: 99}) || selected.IsChanged(token.Position{Filename: "../b/code.go", Line: 3}) || info.Effective != diff.AffectedPackage {
				t.Errorf("deleted path/calling-directory alias scope: %v %+v", selected, info)
			}
		})
	}
}

func TestSelectionPackageMetadataScope(t *testing.T) {
	external := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":  "module example.com/impact/external\n\ngo 1.25.0\n",
		"code.go": "package external\nimport _ \"fmt\"\nfunc Value(a,b int) int {return a+b}\n",
	} {
		if err := os.WriteFile(filepath.Join(external, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mod, write := selectionReviewFixture(t, map[string]string{
		"go.mod":                  "module example.com/impact\n\ngo 1.25.0\nrequire example.com/impact/external v0.0.0\nreplace example.com/impact/external => " + strconv.Quote(filepath.ToSlash(external)) + "\n",
		"consumer/code.go":        "package consumer\nimport _ \"example.com/impact/external\"\nfunc Value(a,b int) int {return a+b}\n",
		"consumer/code_test.go":   "package consumer_test\nimport (_ \"testing\"; _ \"example.com/impact/testdata/helper\")\n",
		"testdata/helper/code.go": "package helper\nfunc Value(a,b int) int {return a+b}\n",
		"unrelated/code.go":       "package unrelated\nfunc Value(a,b int) int {return a+b}\n",
	})
	configuration.Set(configuration.RunDiffScope, diff.ChangedLine)
	write("testdata/helper/code.go", "package helper\nfunc Value(a,b int) int {return a-b}\n")
	selected, _, err := diff.NewSelection(mod)
	if err != nil {
		t.Fatal(err)
	}
	if !selected.IsChanged(token.Position{Filename: "consumer/code.go", Line: 3}) || !selected.IsChanged(token.Position{Filename: "testdata/helper/code.go", Line: 2}) {
		t.Fatalf("owned test-helper metadata lost: %v", selected)
	}
	// Module fallback must retain only canonical production sources, even though
	// go list includes standard dependencies, an overlapping external module path,
	// external-test variants and a synthetic test binary.
	write(".imp.yaml", "{}\n")
	selected, _, err = diff.NewSelection(mod)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[diff.FileName]bool{"consumer/code.go": true, "testdata/helper/code.go": true, "unrelated/code.go": true}
	if len(selected) != len(expected) {
		t.Fatalf("unexpected metadata selected: %v", selected)
	}
	for name := range selected {
		if !expected[name] {
			t.Errorf("non-production/dependency source selected: %s", name)
		}
	}
}
