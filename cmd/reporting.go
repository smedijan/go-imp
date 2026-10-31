package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/report"
)

func configuredReportOptions() (report.Options, error) {
	statuses, err := report.ParseStatuses(configuration.Get[string](configuration.RunStatusesKey))
	if err != nil {
		return report.Options{}, err
	}

	return report.Options{
		JSON:              configuration.Get[bool](configuration.RunJSONKey),
		Verbose:           configuration.Get[bool](configuration.RunVerboseKey),
		DryRun:            configuration.Get[bool](configuration.RunDryRunKey),
		Statuses:          statuses,
		EfficacyThreshold: configuration.Get[float64](configuration.RunThresholdEfficacyKey),
		CoverageThreshold: configuration.Get[float64](configuration.RunThresholdMCoverageKey),
	}, nil
}

func setRunHelp(cmd *cobra.Command) {
	cmd.SetUsageFunc(func(command *cobra.Command) error {
		command.InitDefaultHelpFlag()
		out := command.OutOrStdout()
		fmt.Fprintf(out, "Usage:\n  %s\n\nExamples:\n%s\n", command.UseLine(), command.Example)
		groups := []string{"Reporting", "Scope", "Execution", "Quality gates", "Mutation operators"}
		sets := make(map[string]*pflag.FlagSet)
		for _, group := range groups {
			sets[group] = pflag.NewFlagSet(group, pflag.ContinueOnError)
			sets[group].SortFlags = false
		}
		add := func(flag *pflag.Flag) {
			if !flag.Hidden {
				sets[flagGroup(flag.Name)].AddFlag(flag)
			}
		}
		command.LocalNonPersistentFlags().VisitAll(add)
		command.InheritedFlags().VisitAll(add)
		for _, group := range groups {
			fmt.Fprintf(out, "\n%s:\n%s", group, sets[group].FlagUsagesWrapped(100))
		}
		fmt.Fprintln(out, "\nStatuses: lived, not-covered, timed-out, runnable, killed, skipped, not-viable.")
		fmt.Fprintln(out, "Filters affect findings in every format; aggregate metrics always describe the whole run.")

		return nil
	})
	cmd.SetHelpFunc(func(command *cobra.Command, _ []string) {
		fmt.Fprintln(command.OutOrStdout(), command.Long)
		_ = command.Usage()
	})
}

func flagGroup(name string) string {
	switch name {
	case "json", "output", "statuses", "verbose", "quiet", "no-color", "help":
		return "Reporting"
	case "diff", "diff-scope", "tags", "coverpkg", "exclude-files", "include-files", "exclude-workdir":
		return "Scope"
	case "threshold-efficacy", "threshold-mcover":
		return "Quality gates"
	case "config", "dry-run", "integration", "workers", "test-cpu", "timeout-coefficient":
		return "Execution"
	default:
		return "Mutation operators"
	}
}
