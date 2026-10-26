package report

import (
	"sort"
	"strconv"
	"strings"

	"github.com/smedijan/go-imp/internal/log"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report/internal"
)

func (r *reportStatus) detailedOutput(summary internal.OutputSummary) internal.DetailedOutputResult {
	summary.MutantsTotal = r.lived + r.killed + r.notCovered + r.timedOut + r.skipped + r.notViable + r.runnable
	for _, files := range r.groups {
		for _, findings := range files {
			sort.SliceStable(findings, func(i, j int) bool {
				a, b := findings[i], findings[j]
				switch {
				case a.Line != b.Line:
					return a.Line < b.Line
				case a.Column != b.Column:
					return a.Column < b.Column
				default:
					return a.Type < b.Type
				}
			})
		}
	}

	return internal.DetailedOutputResult{
		OutputSummary:   summary,
		MutantsTimedOut: r.timedOut,
		MutantsSkipped:  r.skipped,
		MutantsRunnable: r.runnable,
		Groups:          r.groups,
	}
}

func detailedReport(mutants []mutator.Mutator) {
	log.Infoln("\nDetailed report (experimental)")
	counts := make(map[mutator.Status]int)
	for _, m := range mutants {
		counts[m.Status()]++
	}
	log.Infof("Total: %d | Survived (LIVED): %d | Not covered: %d | Timed out: %d\n",
		len(mutants), counts[mutator.Lived], counts[mutator.NotCovered], counts[mutator.TimedOut])
	log.Infof("Killed: %d | Skipped: %d | Not viable: %d | Runnable (not tested): %d\n",
		counts[mutator.Killed], counts[mutator.Skipped], counts[mutator.NotViable], counts[mutator.Runnable])
	log.Infoln("Survivors need review, not necessarily a fix: equivalent mutations and intentional behavior are possible.")

	ordered := append([]mutator.Mutator(nil), mutants...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].Position(), ordered[j].Position()
		switch {
		case a.Filename != b.Filename:
			return a.Filename < b.Filename
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Column != b.Column:
			return a.Column < b.Column
		default:
			return ordered[i].Type() < ordered[j].Type()
		}
	})
	descriptions := map[mutator.Status]string{
		mutator.Lived:      "Tests passed; review assertions and edge cases.",
		mutator.NotCovered: "Not exercised by coverage; mutations were not tested.",
		mutator.TimedOut:   "Test execution timed out; not classified as survivors.",
		mutator.Runnable:   "Covered, but mutations were not tested.",
	}
	for _, status := range []mutator.Status{mutator.Lived, mutator.NotCovered, mutator.TimedOut, mutator.Runnable} {
		if counts[status] == 0 {
			continue
		}
		log.Infof("\n%s (%d) - %s\n", status, counts[status], descriptions[status])
		filename := ""
		for _, m := range ordered {
			if m.Status() != status {
				continue
			}
			if m.Position().Filename != filename {
				filename = m.Position().Filename
				log.Infof("  %s\n", filename)
			}
			detailedFinding(m)
		}
	}
	log.Infoln("\nKilled, skipped and invalid mutants are collapsed above. Use --output-statuses ksv for per-mutant output; --output keeps all results as JSON.")
}

func detailedFinding(m mutator.Mutator) {
	pos := m.Position()
	log.Infof("    %d:%d %s\n", pos.Line, pos.Column, m.Type())
	if m.Status() == mutator.Lived {
		if command := testCommand(m); len(command) > 0 {
			log.Infof("      Test command: %s\n", formatCommand(command))
		} else {
			log.Infoln("      Test execution context unavailable.")
		}
	}
	diff, err := generateDiff(m)
	if err != nil || len(diff) == 0 {
		if original := strings.TrimSpace(string(m.OrigSnippet())); original != "" {
			log.Infof("      Source context (no change available):\n        %s\n", strings.ReplaceAll(original, "\n", "\n        "))
		} else {
			log.Infoln("      Source context unavailable.")
		}

		return
	}
	for _, line := range diff {
		log.Infof("        %s\n", line)
	}
}

func testCommand(m mutator.Mutator) []string {
	if context, ok := m.(interface{ TestCommand() []string }); ok {
		return context.TestCommand()
	}

	return nil
}

func formatCommand(command []string) string {
	parts := make([]string, len(command))
	for i, part := range command {
		if strings.ContainsAny(part, " \t\n\"'") {
			part = strconv.Quote(part)
		}
		parts[i] = part
	}

	return strings.Join(parts, " ")
}
