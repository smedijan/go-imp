package log_test

import (
	"bytes"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/log"
)

func TestDiagnosticsDoNotEmitProgressWithoutTerminal(t *testing.T) {
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
	log.Errorf("failure: %s\n", "test")
	if got := stderr.String(); got != "ERROR: failure: test\n" {
		t.Fatalf("stderr = %q", got)
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
