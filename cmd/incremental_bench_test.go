package cmd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/ledger"
)

// BenchmarkCLIIncrementalWorkflow includes real baseline coverage, discovery,
// broad fingerprinting and fresh/reused execution on an eight-mutant module.
func BenchmarkCLIIncrementalWorkflow(b *testing.B) {
	b.Setenv("CGO_ENABLED", "0")
	b.Setenv("GOWORK", "off")
	isolateOutcomeCache(b)
	binary := filepath.Join(b.TempDir(), "imp")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	// #nosec G204 -- the build destination is a benchmark-owned temporary executable.
	build := exec.CommandContext(context.Background(), "go", "build", "-buildvcs=false", "-o", binary, "./imp")
	if output, err := build.CombinedOutput(); err != nil {
		b.Fatalf("build imp: %v\n%s", err, output)
	}
	for _, mode := range []string{"fresh", "confirmed-reuse"} {
		b.Run(mode, func(b *testing.B) {
			dir := b.TempDir()
			var source, tests strings.Builder
			source.WriteString("package workflow\n")
			tests.WriteString("package workflow\nimport \"testing\"\nfunc TestSums(t *testing.T){\n")
			for i := 0; i < 8; i++ {
				fmt.Fprintf(&source, "func Sum%d(a,b int)int{return a+b}\n", i)
				fmt.Fprintf(&tests, "if Sum%d(1,2)!=3{t.Fatal(\"wrong sum\")}\n", i)
			}
			tests.WriteString("}\n")
			for name, contents := range map[string]string{
				"go.mod":       "module example.com/workflow\n\ngo 1.25.0\n",
				".imp.yaml":    "{}\n",
				"code.go":      source.String(),
				"code_test.go": tests.String(),
			} {
				// #nosec G703 -- all fixture paths are within the benchmark's temporary module.
				if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
					b.Fatal(err)
				}
			}
			run := func(refresh bool) {
				b.Helper()
				args := []string{"run", "--json", "--quiet", "--incremental", "--workers=2"}
				if refresh {
					args = append(args, "--incremental-refresh")
				}
				// #nosec G204 -- executes the imp binary built by this benchmark.
				command := exec.CommandContext(context.Background(), binary, args...)
				command.Dir = dir
				out, err := command.Output()
				if err != nil {
					b.Fatal(err)
				}
				var report struct {
					Evidence []ledger.Evidence `json:"evidence"`
					Note     string            `json:"ledger_note"`
				}
				if err = json.Unmarshal(out, &report); err != nil {
					b.Fatal(err)
				}
				if report.Note != "" || len(report.Evidence) != 8 {
					b.Fatalf("unexpected report: %s", out)
				}
				expected := "fresh"
				if !refresh {
					expected = "reused"
				}
				for _, e := range report.Evidence {
					if e.Provenance != expected {
						b.Fatalf("expected %s, got %s", expected, out)
					}
				}
			}
			// Seed two independent fresh observations before timing the reuse workflow.
			if mode == "confirmed-reuse" {
				for i := 0; i < 2; i++ {
					command := exec.CommandContext(context.Background(), binary, "run", "--json", "--quiet", "--incremental", "--workers=2")
					command.Dir = dir
					if output, err := command.CombinedOutput(); err != nil {
						b.Fatalf("seed: %v\n%s", err, output)
					}
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				run(mode == "fresh")
			}
		})
	}
}
