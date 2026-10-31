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

func selectionReviewFixture(t *testing.T, files map[string]string) (gomodule.GoModule, func(string, string)) {
	t.Helper()
	withoutGitEnvironment(t)
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	root := t.TempDir()
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
	write("go.mod", "module example.com/impact\n\ngo 1.25.0\n")
	for name, source := range files {
		write(name, source)
	}
	runGit(t, root, "init")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	configuration.Set(configuration.RunDiffRef, "HEAD")

	return gomodule.GoModule{Root: root, Name: "example.com/impact", CallingDir: "."}, write
}

func TestSelectionChangedFunctionSkipsInvalidInactiveSource(t *testing.T) {
	mod, write := selectionReviewFixture(t, map[string]string{
		"code.go":           "package impact\nfunc Value(a,b int) int {return a+b}\n",
		"inactive.go":       "//go:build impact_inactive\n\npackage impact\nfunc Inactive(a,b int) int {return a+b}\n",
		"dependent/code.go": "package dependent\nimport _ \"example.com/impact\"\nfunc Value(a,b int) int {return a+b}\n",
	})
	write("inactive.go", "//go:build impact_inactive\n\npackage impact\nfunc Inactive(a,b int) int {return a+}\n")
	configuration.Set(configuration.RunDiffScope, diff.ChangedFunction)
	selected, info, err := diff.NewSelection(mod)
	if err != nil {
		t.Fatalf("build-inactive body must not be parsed: %v", err)
	}
	if len(selected) != 0 || info.Effective != "empty" {
		t.Fatalf("inactive change selected production source: %v %+v", selected, info)
	}
}

func TestSelectionTransitiveHiddenTestHelpers(t *testing.T) {
	for _, hidden := range []string{"testdata", "_helpers"} {
		t.Run(hidden, func(t *testing.T) {
			mod, write := selectionReviewFixture(t, map[string]string{
				"consumer/code.go":         "package consumer\nfunc Value(a,b int) int {return a+b}\n",
				"consumer/code_test.go":    "package consumer\nimport _ \"example.com/impact/" + hidden + "/bridge\"\n",
				"external/code.go":         "package external\nfunc Value(a,b int) int {return a+b}\n",
				"external/code_test.go":    "package external_test\nimport _ \"example.com/impact/" + hidden + "/bridge\"\n",
				hidden + "/bridge/code.go": "package bridge\nimport _ \"example.com/impact/" + hidden + "/leaf\"\nfunc Value(a,b int) int {return a+b}\n",
				hidden + "/leaf/code.go":   "package leaf\nfunc Value(a,b int) int {return a+b}\n",
				"unrelated/code.go":        "package unrelated\nfunc Value(a,b int) int {return a+b}\n",
			})
			write(hidden+"/leaf/code.go", "package leaf\nfunc Value(a,b int) int {return a-b}\n")
			configuration.Set(configuration.RunDiffScope, diff.ChangedLine)
			selected, info, err := diff.NewSelection(mod)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"consumer/code.go", "external/code.go", hidden + "/bridge/code.go"} {
				if !selected.IsChanged(token.Position{Filename: name, Line: 3}) {
					t.Errorf("transitive test dependent not selected: %s; %v %+v", name, selected, info)
				}
			}
			if selected.IsChanged(token.Position{Filename: "unrelated/code.go", Line: 2}) {
				t.Error("unrelated package selected")
			}
			for name := range selected {
				if strings.HasSuffix(string(name), "_test.go") || filepath.IsAbs(string(name)) || strings.Contains(string(name), "go-build") {
					t.Errorf("synthetic/test source selected: %s", name)
				}
			}
			// A module-wide fallback must also exclude synthetic test variants and binaries.
			write("settings.json", "{}\n")
			selected, _, err = diff.NewSelection(mod)
			if err != nil {
				t.Fatal(err)
			}
			for name := range selected {
				if strings.HasSuffix(string(name), "_test.go") || filepath.IsAbs(string(name)) || strings.Contains(string(name), "go-build") {
					t.Errorf("fallback selected synthetic/test source: %s", name)
				}
			}
		})
	}
}
