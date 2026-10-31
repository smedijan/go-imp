package cmd //nolint:testpackage // Inject a report writer through the Cobra command seam.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
)

type replayErrorWriter struct{ err error }

func (w replayErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestUnresolvedReplayPreservesReportWriteError(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	root := t.TempDir()
	for name, content := range map[string]string{"go.mod": "module example.test/replay\n\ngo 1.23\n", "source.go": "package replay\nfunc F(a int)int{return a+1}\n", "source_test.go": "package replay\nimport \"testing\"\nfunc TestF(t *testing.T){if F(1)!=2{t.Fatal(\"bad\")}}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	command, err := newRunCmd(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("report sink failed")
	command.cmd.SetOut(replayErrorWriter{err: failure})
	configuration.Set(configuration.RunMutantIDsKey, []string{"imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)})
	configuration.Set(configuration.RunDryRunKey, true)
	err = command.cmd.RunE(command.cmd, nil)
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "write report") {
		t.Fatalf("report write failure masked: %v", err)
	}
}
