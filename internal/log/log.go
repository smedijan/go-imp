// Package log writes diagnostics to stderr, separate from report data.
package log

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"

	"github.com/smedijan/go-imp/internal/configuration"
)

var mutex sync.Mutex
var destination io.Writer

// Init sets the diagnostics destination. Progress is only shown on a terminal.
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

// Infoln displays one interactive informational line.
func Infoln(message any) { Infof("%v\n", message) }

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
