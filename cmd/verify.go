package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/smedijan/go-imp/internal/execution"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/report"
	"github.com/smedijan/go-imp/internal/report/output"
	"github.com/smedijan/go-imp/internal/verification"
)

type verificationError struct{}

func (verificationError) Error() string {
	return "verification did not detect every target; see per-target results"
}
func (verificationError) ExitCode() int { return 1 }

func newVerifyCmd(ctx context.Context) *cobra.Command {
	var options verification.Options
	var prior, destination string
	command := &cobra.Command{
		Use: "verify", Short: "Verify one changed test against exact mutant IDs in isolated snapshots",
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.NoArgs(command, args); err != nil {
				return execution.NewUsageError(err)
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			root, err := os.Getwd()
			if err != nil {
				return err
			}
			mod, err := gomodule.Init(root)
			if err != nil {
				return err
			}
			if filepath.Clean(mod.Root) != filepath.Clean(root) {
				return execution.NewUsageError(fmt.Errorf("run verify at the module root"))
			}
			options.Root = root
			ids := slices.Clone(options.IDs)
			if prior != "" {
				// #nosec G304 -- the user explicitly selects this identity-aware report.
				file, err := os.Open(prior)
				if err != nil {
					return err
				}
				fromReport, readErr := report.ReplayIDs(file, mod.Name)
				closeErr := file.Close()
				if readErr != nil {
					return execution.NewUsageError(readErr)
				}
				if closeErr != nil {
					return closeErr
				}
				ids = append(ids, fromReport...)
			}
			slices.Sort(ids)
			options.IDs = slices.Compact(ids)
			// Open only after snapshot capture, so report staging files do not become
			// part of the test patch. Publication remains atomic on operational errors.
			document, err := verification.Run(ctx, options)
			if err != nil {
				return err
			}
			out, err := output.Open(command.OutOrStdout(), destination)
			if err != nil {
				return err
			}
			defer out.Close()
			if err := json.NewEncoder(out).Encode(document); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := out.Commit(); err != nil {
				return err
			}
			for _, result := range document.Results {
				if result.Outcome != "detected" {
					return verificationError{}
				}
			}

			return nil
		},
	}
	command.Flags().StringVar(&options.Baseline, "baseline", "", "explicit Git test-change baseline commit/ref (required)")
	command.Flags().StringVar(&options.Package, "package", "", "one literal package: . or ./directory (required)")
	command.Flags().StringVar(&options.Test, "test", "", "one changed/new top-level Test name (required)")
	command.Flags().StringSliceVar(&options.IDs, "mutant-ids", nil, "exact mutant IDs, at most 10 (or use --replay-report)")
	command.Flags().StringVar(&prior, "replay-report", "", "identity-aware prior report selecting exact targets")
	command.Flags().DurationVar(&options.Timeout, "timeout", 30*time.Second, "per-attempt timeout (maximum 10m); two attempts per phase")
	command.Flags().StringVarP(&destination, "output", "o", "", "atomic JSON report destination (- or omitted for stdout)")

	return command
}
