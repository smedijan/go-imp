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
