package output_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/internal/report/output"
)

func TestReportOutputStreams(t *testing.T) {
	for _, path := range []string{"", "-"} {
		t.Run(path, func(t *testing.T) {
			var stdout bytes.Buffer
			out, err := output.Open(&stdout, path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(out, "report"); err != nil {
				t.Fatal(err)
			}
			if err := out.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != "report" {
				t.Fatalf("stdout=%q", stdout.String())
			}
			// A caller-owned stream must remain usable after the report is finalized.
			stdout.WriteString(" still open")
			if stdout.String() != "report still open" {
				t.Fatal("stdout was closed")
			}
		})
	}
}

func TestReportOutputFileLifecycle(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			name := map[bool]string{false: "new", true: "existing"}[existing] + "/" + map[bool]string{false: "abort", true: "commit"}[commit]
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				t.Chdir(dir)
				path := filepath.Join(dir, "report.json")
				if existing {
					writeFixture(t, path, "old report")
				}
				var stdout bytes.Buffer
				out, err := output.Open(&stdout, "report.json")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(out, "new report"); err != nil {
					t.Fatal(err)
				}
				assertReportFile(t, path, existing, "old report")
				// Relative destinations are bound at invocation, not after coverage changes cwd.
				other := t.TempDir()
				t.Chdir(other)
				if commit {
					if err := out.Commit(); err != nil {
						t.Fatal(err)
					}
				}
				if err := out.Close(); err != nil {
					t.Fatal(err)
				}
				if err := out.Close(); err != nil {
					t.Fatalf("cleanup must be idempotent: %v", err)
				}
				want := "old report"
				if commit {
					want = "new report"
				}
				assertReportFile(t, path, existing || commit, want)
				if stdout.Len() != 0 {
					t.Error("file report leaked to stdout")
				}
				assertNoTemporaryReports(t, dir)
			})
		}
	}
}

func TestReportOutputFailures(t *testing.T) {
	for _, kind := range []string{"missing parent", "directory target", "replace failure"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "report.json")
			switch kind {
			case "missing parent":
				path = filepath.Join(dir, "absent", "report.json")
			case "directory target":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			out, err := output.Open(io.Discard, path)
			if kind == "missing parent" || kind == "directory target" {
				if err == nil {
					_ = out.Close()
					t.Fatal("invalid target accepted")
				}

				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(out, "new report"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := out.Commit(); err == nil {
				t.Error("commit failure not returned")
			}
			_ = out.Close()
			assertNoTemporaryReports(t, dir)
		})
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertReportFile(t *testing.T, path string, exists bool, want string) {
	t.Helper()
	//nolint:gosec // test-owned output path under t.TempDir
	content, err := os.ReadFile(path)
	if !exists {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("destination must not exist before commit, content %q, error %v", content, err)
		}

		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("report=%q, want %q", content, want)
	}
}

func assertNoTemporaryReports(t *testing.T, dir string) {
	t.Helper()
	leftovers, err := filepath.Glob(filepath.Join(dir, ".imp-report-*"))
	if err != nil || len(leftovers) > 0 {
		t.Fatalf("temporary reports not removed: %v, %v", leftovers, err)
	}
}
