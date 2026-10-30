//nolint:gosec // Fixture paths are created under t.TempDir.
package engine_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestReturnAcceptanceExecutionAndSelection(t *testing.T) {
	returnTestConfig(t)
	configuration.Set(configuration.RunWorkersKey, 1)
	root := t.TempDir()
	originals := map[string][]byte{}
	for _, name := range []string{"go.mod", "returns.go", "returns_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "returns", name))
		if err != nil {
			t.Fatal(err)
		}
		originals[name] = data
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mod := gomodule.GoModule{Name: "example.test/returns", Root: root, CallingDir: "."}
	dealer := fixtureWorkdirDealer{path: root, tempDir: t.TempDir()}
	source := string(originals["returns.go"])
	lines := map[string]int{}
	for _, name := range []string{"AssertedFlag", "IgnoredFlag", "CaughtError", "IgnoredError"} {
		start := strings.Index(source, "func "+name+"(")
		if start < 0 {
			t.Fatalf("fixture function %s missing", name)
		}
		lines[name] = strings.Count(source[:start], "\n") + 1
	}
	cov := coverage.Profile{"returns.go": {{StartLine: 1, StartCol: 1, EndLine: strings.Count(source, "\n") + 1, EndCol: 99}}}
	for _, selection := range []bool{false, true} {
		configuration.Set(configuration.RunDryRunKey, selection)
		data := engine.CodeData{Cov: cov}
		if selection {
			data.Cov = coverage.Profile{"returns.go": {{StartLine: lines["AssertedFlag"], StartCol: 1, EndLine: lines["AssertedFlag"], EndCol: 99}}}
			data.Diff = diff.Diff{"returns.go": {{StartLine: lines["AssertedFlag"], EndLine: lines["IgnoredFlag"]}}}
		}
		executor := engine.NewExecutor(mod, dealer, 10*time.Second)
		e := engine.New(mod, data, executor)
		result, err := e.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Mutants) != 6 {
			t.Fatalf("got %d mutants, want 6", len(result.Mutants))
		}
		counts := map[mutator.Status]int{}
		for _, m := range result.Mutants {
			counts[m.Status()]++
			want := mutator.Lived
			if m.Position().Line == lines["AssertedFlag"] || m.Position().Line == lines["CaughtError"] {
				want = mutator.Killed
			}
			if selection {
				switch m.Position().Line {
				case lines["AssertedFlag"]:
					want = mutator.Runnable
				case lines["IgnoredFlag"]:
					want = mutator.NotCovered
				default:
					want = mutator.Skipped
				}
			}
			if m.Status() != want {
				t.Errorf("%s: got %s, want %s", m.Position(), m.Status(), want)
			}
			if !bytes.Contains(m.OrigSnippet(), []byte("return")) || bytes.Equal(m.OrigSnippet(), m.MutatedSnippet()) {
				t.Fatal("missing or identical discovery snippets")
			}
			if !selection {
				recorder, ok := m.(interface{ TestCommand() []string })
				if !ok || !strings.Contains(strings.Join(recorder.TestCommand(), " "), "example.test/returns") {
					t.Fatal("missing test command")
				}
			}
		}
		t.Logf("selection=%v statuses=%v elapsed=%s", selection, counts, result.Elapsed)
		for name, original := range originals {
			restored, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || !bytes.Equal(restored, original) {
				t.Fatalf("sequential worker did not restore %s: %v", name, err)
			}
		}
	}
}
