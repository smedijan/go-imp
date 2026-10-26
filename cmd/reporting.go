package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

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

// reportOutput stages file reports next to the destination. Until Commit succeeds,
// failures and cancellation leave any previous report untouched.
type reportOutput struct {
	io.Writer
	file      *os.File
	temporary string
	target    string
}

func openReportOutput(stdout io.Writer, path string) (*reportOutput, error) {
	if path == "" || path == "-" {
		return &reportOutput{Writer: stdout}, nil
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve report %q: %w", path, err)
	}
	if err := validateReportTarget(target); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".imp-report-*")
	if err != nil {
		return nil, fmt.Errorf("open report %q: %w", path, err)
	}

	return &reportOutput{Writer: file, file: file, temporary: file.Name(), target: target}, nil
}

func validateReportTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect report %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("report destination %q is not a regular file; use --output - and shell redirection for pipes or devices", path)
	}

	return nil
}

func (out *reportOutput) Commit() error {
	if out.temporary == "" {
		return nil
	}
	if out.file != nil {
		err := out.file.Close()
		out.file = nil
		if err != nil {
			return fmt.Errorf("close report: %w", err)
		}
	}
	if err := validateReportTarget(out.target); err != nil {
		return err
	}
	if err := os.Rename(out.temporary, out.target); err != nil {
		return fmt.Errorf("replace report: %w", err)
	}
	out.temporary = ""

	return nil
}

func (out *reportOutput) Close() error {
	var closeErr, removeErr error
	if out.file != nil {
		closeErr = out.file.Close()
		out.file = nil
	}
	if out.temporary != "" {
		removeErr = os.Remove(out.temporary)
		out.temporary = ""
	}

	return errors.Join(closeErr, removeErr)
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
	case "diff", "tags", "coverpkg", "exclude-files":
		return "Scope"
	case "threshold-efficacy", "threshold-mcover":
		return "Quality gates"
	case "config", "dry-run", "integration", "workers", "test-cpu", "timeout-coefficient":
		return "Execution"
	default:
		return "Mutation operators"
	}
}
