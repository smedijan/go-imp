package cmd

import (
	"bytes"
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
			root, err := newRootCmd(context.Background(), "test")
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			root.cmd.SetOut(&out)
			root.cmd.SetArgs([]string{"run", "--config", path, "--json"})
			err = root.execute()
			var usage *execution.UsageError
			if !errors.As(err, &usage) || usage.ExitCode() != 2 {
				t.Fatalf("expected invalid-configuration exit2, got %v", err)
			}
			if out.Len() != 0 {
				t.Errorf("invalid configuration must not emit a report: %s", &out)
			}
		})
	}
}
