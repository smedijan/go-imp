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

func TestSelectionNonGoInputs(t *testing.T) {
	for _, tc := range []struct {
		name, path, scope, effective, reason       string
		wantConsumer, wantDependent, wantUnrelated bool
	}{
		{name: "readme", path: "README.md", scope: diff.ChangedLine, effective: "empty", reason: "ignored documentation/metadata"},
		{name: "docs schema", path: "docs/schema/configuration.json", scope: diff.ChangedLine, effective: "empty", reason: "ignored documentation/metadata"},
		{name: "function docs", path: "docs/guide.md", scope: diff.ChangedFunction, effective: "empty", reason: "ignored documentation/metadata"},
		{name: "git metadata", path: ".gitignore", scope: diff.ChangedLine, effective: "empty", reason: "ignored documentation/metadata"},
		{name: "broad package policy", path: "README.md", scope: diff.AffectedPackage, effective: "module", reason: "requested affected-package non-Go fallback", wantConsumer: true, wantDependent: true, wantUnrelated: true},
		{name: "config", path: ".imp.yaml", scope: diff.ChangedLine, effective: "module", reason: "build/configuration change", wantConsumer: true, wantDependent: true, wantUnrelated: true},
		{name: "build", path: "Makefile", scope: diff.ChangedFunction, effective: "module", reason: "build/configuration change", wantConsumer: true, wantDependent: true, wantUnrelated: true},
		{name: "fixture markdown", path: "consumer/testdata/README.md", scope: diff.ChangedLine, effective: diff.AffectedPackage, reason: "test fixture input", wantConsumer: true, wantDependent: true},
		{name: "native build", path: "consumer/ops.s", scope: diff.ChangedLine, effective: diff.AffectedPackage, reason: "native build input", wantConsumer: true, wantDependent: true},
		{name: "fixture", path: "consumer/testdata/input.json", scope: diff.ChangedLine, effective: diff.AffectedPackage, reason: "test fixture input", wantConsumer: true, wantDependent: true},
		{name: "embedded docs", path: "consumer/docs/schema.json", scope: diff.ChangedLine, effective: diff.AffectedPackage, reason: "embedded input", wantConsumer: true, wantDependent: true},
		{name: "external test embed", path: "consumer/docs/test.txt", scope: diff.ChangedFunction, effective: diff.AffectedPackage, reason: "embedded input", wantConsumer: true, wantDependent: true},
		{name: "internal test embed", path: "consumer/docs/internal.txt", scope: diff.ChangedLine, effective: diff.AffectedPackage, reason: "embedded input", wantConsumer: true, wantDependent: true},
		{name: "unknown package input", path: "consumer/runtime.data", scope: diff.ChangedLine, effective: diff.AffectedPackage, reason: "unknown input: conservative package fallback", wantConsumer: true, wantDependent: true},
		{name: "unknown root input", path: "runtime.data", scope: diff.ChangedLine, effective: "module", reason: "unknown input: conservative module fallback", wantConsumer: true, wantDependent: true, wantUnrelated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod, write := selectionReviewFixture(t, map[string]string{
				"consumer/code.go":           "package consumer\nimport _ \"embed\"\n//go:embed docs/schema.json\nvar Schema string\nfunc Value(a,b int) int {return a+b}\n",
				"consumer/code_test.go":      "package consumer\nimport _ \"embed\"\n//go:embed docs/internal.txt\nvar Internal string\n",
				"consumer/external_test.go":  "package consumer_test\nimport _ \"embed\"\n//go:embed docs/test.txt\nvar TestInput string\n",
				"consumer/docs/schema.json":  "{}\n",
				"consumer/docs/test.txt":     "test\n",
				"consumer/docs/internal.txt": "internal\n",
				"dependent/code.go":          "package dependent\nimport _ \"example.com/impact/consumer\"\nfunc Value(a,b int) int {return a+b}\n",
				"unrelated/code.go":          "package unrelated\nfunc Value(a,b int) int {return a+b}\n",
			})
			write(tc.path, "# changed input\n")
			configuration.Set(configuration.RunDiffScope, tc.scope)
			selected, info, err := diff.NewSelection(mod)
			if err != nil {
				t.Fatal(err)
			}
			if info.Requested != tc.scope || info.Effective != tc.effective || !strings.Contains(strings.Join(info.Reasons, ";"), tc.reason) {
				t.Errorf("scope/reason: %+v, want requested=%s effective=%s reason=%s", info, tc.scope, tc.effective, tc.reason)
			}
			for name, want := range map[string]bool{"consumer/code.go": tc.wantConsumer, "dependent/code.go": tc.wantDependent, "unrelated/code.go": tc.wantUnrelated} {
				if got := selected.IsChanged(token.Position{Filename: name, Line: 99}); got != want {
					t.Errorf("selected %s=%t want %t: %v", name, got, want, selected)
				}
			}
		})
	}
}

func TestSelectionDocumentationDoesNotBroadenSourceChange(t *testing.T) {
	mod, write := selectionReviewFixture(t, map[string]string{
		"code.go":           "package impact\nfunc First(a,b int) int {return a+b}\nfunc Second(a,b int) int {return a-b}\n",
		"unrelated/code.go": "package unrelated\nfunc Value(a,b int) int {return a+b}\n",
	})
	write("code.go", "package impact\nfunc First(a,b int) int {return a-b}\nfunc Second(a,b int) int {return a-b}\n")
	write("docs/schema/configuration.json", "{}\n")
	configuration.Set(configuration.RunDiffScope, diff.ChangedLine)
	selected, info, err := diff.NewSelection(mod)
	if err != nil {
		t.Fatal(err)
	}
	if !selected.IsChanged(token.Position{Filename: "code.go", Line: 2}) || selected.IsChanged(token.Position{Filename: "code.go", Line: 3}) || selected.IsChanged(token.Position{Filename: "unrelated/code.go", Line: 2}) || info.Effective != diff.ChangedLine {
		t.Fatalf("documentation broadened changed-line scope: %v %+v", selected, info)
	}
}

func TestSelectionDeletedEmbeddedDocumentation(t *testing.T) {
	for _, scope := range []string{diff.ChangedLine, diff.ChangedFunction} {
		for _, tc := range []struct {
			name, pattern, source, packageName, removed string
			rename                                      bool
		}{
			{name: "production glob deletion", pattern: "docs/*.md", source: "consumer/embed.go", packageName: "consumer", removed: "docs/a.md"},
			{name: "production glob rename", pattern: "docs/*.md", source: "consumer/embed.go", packageName: "consumer", removed: "docs/a.md", rename: true},
			{name: "internal test glob", pattern: "docs/*.md", source: "consumer/embed_test.go", packageName: "consumer", removed: "docs/a.md"},
			{name: "external test glob", pattern: "docs/*.md", source: "consumer/embed_test.go", packageName: "consumer_test", removed: "docs/a.md"},
			{name: "directory glob deletion", pattern: "docs/*", source: "consumer/embed.go", packageName: "consumer", removed: "docs/nested/a.md"},
			{name: "all glob deletion", pattern: "all:docs/*.md", source: "consumer/embed_test.go", packageName: "consumer", removed: "docs/_a.md"},
			{name: "directory deletion", pattern: "docs", source: "consumer/embed.go", packageName: "consumer", removed: "docs/nested/a.md"},
			{name: "all directory rename", pattern: "all:docs", source: "consumer/embed_test.go", packageName: "consumer_test", removed: "docs/_hidden/a.md", rename: true},
		} {
			t.Run(scope+"/"+tc.name, func(t *testing.T) {
				mod, _ := selectionReviewFixture(t, map[string]string{
					"consumer/code.go":           "package consumer\nfunc Value(a,b int) int {return a+b}\n",
					tc.source:                    "package " + tc.packageName + "\nimport \"embed\"\n//go:embed " + tc.pattern + "\nvar Files embed.FS\n",
					"consumer/" + tc.removed:     "removed embedded documentation\n",
					"consumer/docs/b.md":         "surviving embedded documentation\n",
					"consumer/docs/nested/b.md":  "surviving nested documentation\n",
					"consumer/docs/_hidden/b.md": "surviving hidden documentation\n",
					"dependent/code.go":          "package dependent\nimport _ \"example.com/impact/consumer\"\nfunc Value(a,b int) int {return a+b}\n",
					"unrelated/code.go":          "package unrelated\nfunc Value(a,b int) int {return a+b}\n",
				})
				old := filepath.Join(mod.Root, "consumer", filepath.FromSlash(tc.removed))
				if tc.rename {
					runGit(t, mod.Root, "mv", filepath.ToSlash(filepath.Join("consumer", tc.removed)), "consumer/README.md")
				} else if err := os.Remove(old); err != nil {
					t.Fatal(err)
				}
				configuration.Set(configuration.RunDiffScope, scope)
				selected, info, err := diff.NewSelection(mod)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"consumer/code.go", "dependent/code.go"} {
					if !selected.IsChanged(token.Position{Filename: name, Line: 99}) {
						t.Errorf("deleted/renamed embed owner/dependent not selected: %s: %v %+v", name, selected, info)
					}
				}
				if selected.IsChanged(token.Position{Filename: "unrelated/code.go", Line: 99}) {
					t.Error("unrelated package selected")
				}
				if info.Effective != diff.AffectedPackage || !strings.Contains(strings.Join(info.Reasons, ";"), "embedded input pattern: conservative ownership fallback: consumer/"+tc.removed) {
					t.Errorf("missing effective scope/pattern fallback reason: %+v", info)
				}
			})
		}
	}
}
