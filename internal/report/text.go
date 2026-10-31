package report

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/aymanbagabas/go-udiff"
	"github.com/fatih/color"
	"github.com/hako/durafmt"

	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report/internal"
)

var descriptions = map[mutator.Status]string{
	mutator.Lived:      "Tests passed; review assertions and edge cases.",
	mutator.NotCovered: "Not exercised by coverage; mutations were not tested.",
	mutator.TimedOut:   "Test execution timed out; not classified as survivors.",
	mutator.Runnable:   "Covered, but mutations were not tested.",
	mutator.Killed:     "Tests detected these mutations.",
	mutator.Skipped:    "Excluded by the diff scope; mutations were not tested.",
	mutator.NotViable:  "Mutations could not be built or executed as valid tests.",
}

func writeText(out io.Writer, document internal.OutputResult, mutants []mutator.Mutator, options Options) {
	for _, target := range document.Replay {
		fmt.Fprintf(out, "Replay %s: %s\n", target.ID, target.Resolution)
	}
	title := "Mutation testing"
	if options.DryRun {
		title = "Dry run"
	}
	if len(document.Groups) == 0 {
		fmt.Fprintln(out, "\nNo findings for the selected statuses.")
	}
	for _, status := range options.Statuses {
		files := document.Groups[status.String()]
		count := 0
		for _, findings := range files {
			count += len(findings)
		}
		if count == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s (%d) - %s\n", paint(status.String(), statusColor(status), options.Color), count, descriptions[status])
		filename := ""
		for _, m := range mutants {
			if m.Status() != status {
				continue
			}
			if m.Position().Filename != filename {
				filename = m.Position().Filename
				fmt.Fprintf(out, "  %s\n", filename)
			}
			writeFinding(out, m, options)
		}
	}

	elapsed := durafmt.Parse(time.Duration(document.ElapsedTime * float64(time.Second))).LimitFirstN(2)
	fmt.Fprintf(out, "\n%s completed in %s\n", title, elapsed)
	fmt.Fprintf(out, "Total: %d | Lived: %d | Not covered: %d | Timed out: %d\n",
		document.MutantsTotal, document.MutantsLived, document.MutantsNotCovered, document.MutantsTimedOut)
	fmt.Fprintf(out, "Killed: %d | Skipped: %d | Not viable: %d | Runnable: %d\n",
		document.MutantsKilled, document.MutantsSkipped, document.MutantsNotViable, document.MutantsRunnable)
	fmt.Fprintf(out, "Test efficacy: %.2f%%\nMutator coverage: %.2f%%\n", document.TestEfficacy, document.MutationsCoverage)
	if document.MutantsLived > 0 && selected(options.Statuses, mutator.Lived) {
		fmt.Fprintln(out, "Survivors require judgment: equivalent mutations and intentional behavior are possible.")
	}
}

func writeFinding(out io.Writer, m mutator.Mutator, options Options) {
	pos := m.Position()
	fmt.Fprintf(out, "    %d:%d %s\n", pos.Line, pos.Column, m.Type())
	if options.Verbose || m.Status() == mutator.Lived {
		if command := testCommand(m); len(command) > 0 {
			fmt.Fprintf(out, "      Test command: %s\n", formatCommand(command))
		}
	}
	if !options.Verbose && m.Status() != mutator.Lived && m.Status() != mutator.NotCovered {
		return
	}
	diff := generateDiff(m, options.Color)
	if len(diff) == 0 {
		return
	}
	for _, line := range diff {
		fmt.Fprintf(out, "        %s\n", line)
	}
}

func generateDiff(m mutator.Mutator, colors bool) []string {
	original, mutated := m.OrigSnippet(), m.MutatedSnippet()
	if len(original) == 0 || len(mutated) == 0 {
		return nil
	}
	diff, err := udiff.ToUnified("Original", "Mutated", string(original), udiff.Bytes(original, mutated), 3)
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case line == "", strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"),
			strings.HasPrefix(line, "@@"), strings.Contains(line, "No newline at end of file"):
			continue
		case strings.HasPrefix(line, "-"):
			lines = append(lines, paint(line, color.FgRed, colors))
		case strings.HasPrefix(line, "+"):
			lines = append(lines, paint(line, color.FgGreen, colors))
		default:
			lines = append(lines, line)
		}
	}

	return lines
}

func statusColor(status mutator.Status) color.Attribute {
	switch status {
	case mutator.Lived:
		return color.FgRed
	case mutator.NotCovered, mutator.TimedOut:
		return color.FgYellow
	case mutator.Killed:
		return color.FgGreen
	case mutator.Runnable, mutator.Skipped, mutator.NotViable:
		return color.FgHiBlack
	}

	return color.Reset
}

func paint(text string, attribute color.Attribute, enabled bool) string {
	if !enabled {
		return text
	}
	style := color.New(attribute)
	style.EnableColor()

	return style.Sprint(text)
}

func testCommand(m mutator.Mutator) []string {
	if context, ok := m.(interface{ TestCommand() []string }); ok {
		return context.TestCommand()
	}

	return nil
}

var shellWord = regexp.MustCompile(`^[a-zA-Z0-9_@%+=:,./-]+$`)

// formatCommand uses POSIX shell quoting, never Go string literal escaping.
func formatCommand(command []string) string {
	parts := make([]string, len(command))
	for i, part := range command {
		if !shellWord.MatchString(part) {
			part = "'" + strings.ReplaceAll(part, "'", "'\"'\"'") + "'"
		}
		parts[i] = part
	}

	return strings.Join(parts, " ")
}
