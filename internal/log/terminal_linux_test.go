package log_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/log"
)

type terminalBuffer struct {
	bytes.Buffer
	fd uintptr
}

func (buffer *terminalBuffer) Fd() uintptr { return buffer.fd }

func TestTerminalProgressSettings(t *testing.T) {
	terminal, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	for _, kind := range []string{"terminal", "redirected", "unset"} {
		for _, quiet := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/quiet=%t", kind, quiet), func(t *testing.T) {
				configuration.Reset()
				t.Cleanup(configuration.Reset)
				configuration.Set(configuration.ImpQuietKey, quiet)
				t.Setenv("NO_COLOR", "1")
				var capture terminalBuffer
				capture.fd = terminal.Fd()
				var writer io.Writer = &capture
				if kind == "redirected" {
					writer = &capture.Buffer
				}
				if kind == "unset" {
					writer = nil
				}
				log.Init(writer)
				t.Cleanup(log.Reset)
				log.Infof("progress %d\n", 1)
				log.Errorln("failure")
				want := "ERROR: failure\n"
				if kind == "terminal" && !quiet {
					want = "progress 1\n" + want
				}
				if kind == "unset" {
					want = ""
				}
				if got := capture.String(); got != want {
					t.Fatalf("diagnostics=%q, want %q", got, want)
				}
			})
		}
	}
}

func TestTerminalColorSettings(t *testing.T) {
	// A PTY master is a real terminal descriptor, unlike the redirected test stdout.
	terminal, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	if !log.IsTerminal(terminal) {
		t.Fatal("PTY descriptor must be recognized as a terminal")
	}
	for _, term := range []string{"xterm-256color", "dumb", ""} {
		for _, noColor := range []string{"absent", "empty", "set"} {
			for _, disabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("TERM=%s/NO_COLOR=%s/disabled=%t", term, noColor, disabled), func(t *testing.T) {
					configuration.Reset()
					t.Cleanup(configuration.Reset)
					configuration.Set(configuration.ImpNoColorKey, disabled)
					t.Setenv("TERM", term)
					t.Setenv("NO_COLOR", "")
					switch noColor {
					case "absent":
						if err := os.Unsetenv("NO_COLOR"); err != nil {
							t.Fatal(err)
						}
					case "set":
						t.Setenv("NO_COLOR", "1")
					}
					want := term != "dumb" && noColor == "absent" && !disabled
					if got := log.ColorEnabled(terminal); got != want {
						t.Errorf("color enabled=%t, want %t", got, want)
					}
				})
			}
		}
	}
}
