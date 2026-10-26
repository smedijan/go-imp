package cmd_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlackBoxLintPolicy(t *testing.T) {
	lint, err := exec.LookPath("golangci-lint")
	if err != nil {
		t.Skip("golangci-lint is installed by mise, optional for Go-only environments")
	}
	config, err := filepath.Abs("../.golangci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"example", "main"} {
		t.Run(pkg, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/lintpolicy\n\ngo 1.25.0\n")
			writeFixture(t, filepath.Join(dir, "source.go"), "package "+pkg+"\n")
			for _, name := range []string{"source_test.go", "export_test.go", "source_internal_test.go"} {
				writeFixture(t, filepath.Join(dir, name), "package "+pkg+"\n")
			}
			run := func() ([]byte, error) {
				//nolint:gosec // executable is resolved by LookPath; fixture and arguments are test-owned
				command := exec.Command(lint, "run", "--config", config, "--enable-only", "testpackage", "--max-same-issues", "0", "--max-issues-per-linter", "0", "./...")
				command.Dir = dir

				return command.CombinedOutput()
			}
			output, err := run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("expected lint violations, got %v:\n%s", err, output)
			}
			for _, name := range []string{"source_test.go", "export_test.go", "source_internal_test.go"} {
				if !strings.Contains(string(output), name+":1:9: package should be `"+pkg+"_test`") {
					t.Errorf("%s escaped enforcement:\n%s", name, output)
				}
				writeFixture(t, filepath.Join(dir, name), "package "+pkg+"_test\n")
			}
			if output, err := run(); err != nil {
				t.Fatalf("external tests rejected: %v\n%s", err, output)
			}
		})
	}
}
