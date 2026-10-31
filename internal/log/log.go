// Package log writes diagnostics to stderr, separate from report data.
package log

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"

	"github.com/smedijan/go-imp/internal/configuration"
)

var mutex sync.Mutex
var destination io.Writer

// Init sets the stderr destination for progress and diagnostic messages.
func Init(stderr io.Writer) {
	mutex.Lock()
	defer mutex.Unlock()
	destination = stderr
}

// Reset removes the diagnostics destination, primarily for tests.
func Reset() { Init(nil) }

// IsTerminal reports whether a writer is connected to a terminal.
func IsTerminal(writer io.Writer) bool {
	file, ok := writer.(interface{ Fd() uintptr })
	if !ok {
		return false
	}

	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}

// ColorEnabled respects explicit configuration and common terminal conventions.
func ColorEnabled(writer io.Writer) bool {
	_, noColor := os.LookupEnv("NO_COLOR")

	return IsTerminal(writer) && !noColor && os.Getenv("TERM") != "dumb" && !configuration.Get[bool](configuration.ImpNoColorKey)
}

// Infof displays interactive progress unless quiet mode is enabled.
func Infof(format string, args ...any) {
	mutex.Lock()
	defer mutex.Unlock()
	if destination == nil || !IsTerminal(destination) || configuration.Get[bool](configuration.ImpQuietKey) {
		return
	}
	_, _ = fmt.Fprintf(destination, format, args...)
}

// Summaryf writes concise diagnostics to stderr, including redirected output,
// unless quiet mode is enabled.
func Summaryf(format string, args ...any) {
	mutex.Lock()
	defer mutex.Unlock()
	if destination == nil || configuration.Get[bool](configuration.ImpQuietKey) {
		return
	}
	_, _ = fmt.Fprintf(destination, format, args...)
}

// Infoln displays one interactive informational line.
func Infoln(message any) { Infof("%v\n", message) }

// FileProgress renders a terminal-only progress bar based on completed source files.
type FileProgress struct {
	total    int
	complete int
	drawn    int
	enabled  bool
	terminal bool
	finished bool
}

// NewFileProgress reports progress based on each completed file's proportional
// share of the total. Terminals get a 100-character bar; other writers get
// concise checkpoints to avoid flooding captured output.
func NewFileProgress(total int) *FileProgress {
	progress := &FileProgress{total: total}
	mutex.Lock()
	defer mutex.Unlock()
	progress.enabled = total > 0 && destination != nil && !configuration.Get[bool](configuration.ImpQuietKey)
	progress.terminal = progress.enabled && IsTerminal(destination)
	if progress.terminal {
		writeProgress(destination, 0, total)
	} else if progress.enabled {
		_, _ = fmt.Fprintf(destination, "Progress: 0%% (0/%d files)\n", total)
	}

	return progress
}

// Complete marks one source file complete and emits progress when the display
// reaches its next percentage point or captured-output checkpoint.
func (p *FileProgress) Complete() {
	mutex.Lock()
	defer mutex.Unlock()
	if !p.enabled || p.finished || p.complete >= p.total {
		return
	}
	p.complete++
	filled := p.complete * 100 / p.total
	if p.terminal {
		if filled > p.drawn {
			writeProgress(destination, p.complete, p.total)
			p.drawn = filled
		}

		return
	}
	if filled/5 > p.drawn/5 {
		_, _ = fmt.Fprintf(destination, "Progress: %d%% (%d/%d files)\n", filled, p.complete, p.total)
		p.drawn = filled
	}
}

// Finish finalizes the progress display, ending an interactive bar line.
func (p *FileProgress) Finish() {
	mutex.Lock()
	defer mutex.Unlock()
	if p.enabled && !p.finished {
		if p.terminal {
			_, _ = fmt.Fprintln(destination)
		}
		p.finished = true
	}
}

func writeProgress(out io.Writer, complete, total int) {
	filled := complete * 100 / total
	bar := strings.Repeat("#", filled) + strings.Repeat("-", 100-filled)
	_, _ = fmt.Fprintf(out, "\r[%s] %3d%% (%d/%d files)", bar, filled, complete, total)
}

// Errorf always writes an error, including in quiet mode and redirected output.
func Errorf(format string, args ...any) {
	mutex.Lock()
	defer mutex.Unlock()
	if destination == nil {
		return
	}
	label := "ERROR"
	if ColorEnabled(destination) {
		style := color.New(color.FgRed)
		style.EnableColor()
		label = style.Sprint(label)
	}
	_, _ = fmt.Fprintf(destination, "%s: %s", label, fmt.Sprintf(format, args...))
}

// Errorln always writes an error line.
func Errorln(message any) { Errorf("%v\n", message) }
