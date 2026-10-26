package log_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/log"
)

func TestInteractiveProgressDoesNotEmitWithoutTerminal(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	var stderr bytes.Buffer
	log.Init(&stderr)
	t.Cleanup(log.Reset)
	log.Infof("progress %d", 1)
	log.Infoln("more progress")
	log.Errorln("failed")
	if got := stderr.String(); got != "ERROR: failed\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestQuietDoesNotHideErrors(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	configuration.Set(configuration.ImpQuietKey, true)
	var stderr bytes.Buffer
	log.Init(&stderr)
	t.Cleanup(log.Reset)
	log.Infof("progress")
	progress := log.NewFileProgress(2)
	progress.Complete()
	progress.Complete()
	progress.Finish()
	log.Errorf("failure: %s\n", "test")
	if got := stderr.String(); got != "ERROR: failure: test\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestCapturedFileProgressUsesFivePercentCheckpoints(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	var stderr bytes.Buffer
	log.Init(&stderr)
	t.Cleanup(log.Reset)

	progress := log.NewFileProgress(100)
	for i := 0; i < 100; i++ {
		progress.Complete()
	}
	progress.Finish()

	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 21 {
		t.Fatalf("got %d checkpoints, want 21 including the start: %q", len(lines), stderr.String())
	}
	if lines[0] != "Progress: 0% (0/100 files)" {
		t.Errorf("initial progress = %q", lines[0])
	}
	for i, line := range lines[1:] {
		want := fmt.Sprintf("Progress: %d%% (%d/100 files)", (i+1)*5, (i+1)*5)
		if line != want {
			t.Errorf("checkpoint %d = %q, want %q", i, line, want)
		}
	}
}

func TestNoLoggerAndColorConventions(t *testing.T) {
	log.Reset()
	log.Infof("ignored")
	log.Errorln("ignored")
	t.Setenv("NO_COLOR", "1")
	if log.ColorEnabled(&bytes.Buffer{}) || log.IsTerminal(&bytes.Buffer{}) {
		t.Fatal("buffer must not be treated as a colored terminal")
	}
}
