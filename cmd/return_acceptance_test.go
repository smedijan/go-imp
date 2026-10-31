//nolint:gosec // Commands and paths belong to the test's temporary fixture.
package cmd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/mutator"
)

func TestReturnCLIQuality(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "imp.exe")
	build := exec.Command("go", "build", "-o", binary, "./imp")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	dir := t.TempDir()
	originals := map[string][]byte{}
	for _, name := range []string{"go.mod", "returns.go", "returns_test.go"} {
		data, err := os.ReadFile(filepath.Join("..", "internal", "engine", "testdata", "returns", name))
		if err != nil {
			t.Fatal(err)
		}
		originals[name] = data
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(t, filepath.Join(dir, ".imp.yaml"), "{}\n")
	for _, tc := range []struct {
		name                           string
		boolean, errors, dry           bool
		total, killed, lived, runnable int
	}{
		{name: "baseline"},
		{name: "boolean", boolean: true, total: 4, killed: 2, lived: 2},
		{name: "error", errors: true, total: 2, killed: 1, lived: 1},
		{name: "combined", boolean: true, errors: true, total: 6, killed: 3, lived: 3},
		{name: "combined-dry", boolean: true, errors: true, dry: true, total: 6, runnable: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"run", "--json", "--quiet", "--workers=1", fmt.Sprintf("--dry-run=%v", tc.dry)}
			for _, mt := range mutator.Types {
				enabled := (mt == mutator.BooleanReturn && tc.boolean) || (mt == mutator.ErrorReturn && tc.errors)
				args = append(args, fmt.Sprintf("--%s=%v", strings.ReplaceAll(strings.ToLower(mt.String()), "_", "-"), enabled))
			}
			command := exec.Command(binary, args...)
			command.Dir = dir
			var stderr bytes.Buffer
			command.Stderr = &stderr
			start := time.Now()
			output, err := command.Output()
			wall := time.Since(start)
			if err != nil {
				t.Fatalf("CLI: %v\n%s\n%s", err, output, stderr.String())
			}
			var report struct {
				Total     int     `json:"mutants_total"`
				Killed    int     `json:"mutants_killed"`
				Lived     int     `json:"mutants_lived"`
				Runnable  int     `json:"mutants_runnable"`
				Invalid   int     `json:"mutants_not_viable"`
				Uncovered int     `json:"mutants_not_covered"`
				Skipped   int     `json:"mutants_skipped"`
				TimedOut  int     `json:"mutants_timed_out"`
				Elapsed   float64 `json:"elapsed_time"`
			}
			if err := json.Unmarshal(output, &report); err != nil {
				t.Fatalf("decode: %v\n%s", err, output)
			}
			if report.Total != tc.total || report.Killed != tc.killed || report.Lived != tc.lived || report.Runnable != tc.runnable || report.Invalid+report.Uncovered+report.Skipped+report.TimedOut != 0 {
				t.Fatalf("unexpected quality: %+v", report)
			}
			t.Logf("total=%d killed=%d lived=%d runnable=%d invalid=%d engine=%.6fs wall=%s", report.Total, report.Killed, report.Lived, report.Runnable, report.Invalid, report.Elapsed, wall)
			for name, original := range originals {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || !bytes.Equal(got, original) {
					t.Fatalf("source changed: %s (%v)", name, err)
				}
			}
		})
	}
}
