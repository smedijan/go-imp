package cmd_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/execution"
)

func TestInvalidConfigurationStopsBeforeExecution(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		exists  bool
	}{
		{name: "missing file"},
		{name: "malformed YAML", content: "run: [broken", exists: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			path := filepath.Join(t.TempDir(), ".imp.yaml")
			if tc.exists {
				writeFixture(t, path, tc.content)
			}
			out, err := executeCLI(context.Background(), t, "test", "run", "--config", path, "--json")
			var usage *execution.UsageError
			if !errors.As(err, &usage) || usage.ExitCode() != 2 {
				t.Fatalf("expected invalid-configuration exit2, got %v", err)
			}
			if out != "" {
				t.Errorf("invalid configuration must not emit a report: %s", out)
			}
		})
	}
}
