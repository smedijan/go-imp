/*
 * Copyright 2022 The Gremlins Authors
 *
 *    Licensed under the Apache License, Version 2.0 (the "License");
 *    you may not use this file except in compliance with the License.
 *    You may obtain a copy of the License at
 *
 *        http://www.apache.org/licenses/LICENSE-2.0
 *
 *    Unless required by applicable law or agreed to in writing, software
 *    distributed under the License is distributed on an "AS IS" BASIS,
 *    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *    See the License for the specific language governing permissions and
 *    limitations under the License.
 */

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/smedijan/go-imp/cmd/internal/flags"
	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/exclusion"
	"github.com/smedijan/go-imp/internal/execution"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/log"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
	"github.com/smedijan/go-imp/internal/report/output"
)

type runCmd struct {
	cmd *cobra.Command
}

const (
	commandName = "run"

	paramDiff               = "diff"
	paramBuildTags          = "tags"
	paramCoverPackages      = "coverpkg"
	paramDryRun             = "dry-run"
	paramOutput             = "output"
	paramIntegrationMode    = "integration"
	paramExcludeFiles       = "exclude-files"
	paramExcludeWorkdir     = "exclude-workdir"
	paramTestCPU            = "test-cpu"
	paramWorkers            = "workers"
	paramTimeoutCoefficient = "timeout-coefficient"

	// Thresholds.
	paramThresholdEfficacy  = "threshold-efficacy"
	paramThresholdMCoverage = "threshold-mcover"
)

func newRunCmd(ctx context.Context) (*runCmd, error) {
	cmd := &cobra.Command{
		Use:     fmt.Sprintf("%s [path]", commandName),
		Aliases: []string{"r"},
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return execution.NewUsageError(err)
			}

			return nil
		},
		Short:   "Run mutation tests on your Go code",
		Long:    longExplainer(),
		Example: "  imp run\n  imp run --statuses lived,not-covered\n  imp run --json --statuses lived --output survivors.json\n  imp run --dry-run --json",
		PreRunE: func(_ *cobra.Command, _ []string) error {
			_, err := configuredReportOptions()
			if err != nil {
				return execution.NewUsageError(err)
			}

			return nil
		},
		RunE: runMutationTests(ctx),
	}

	if err := setFlagsOnCmd(cmd); err != nil {
		return nil, err
	}

	setRunHelp(cmd)

	return &runCmd{cmd: cmd}, nil
}

func longExplainer() string {
	return heredoc.Doc(`
		Test whether your tests detect mutations in a Go module.
		Only covered mutants are executed; --dry-run gathers coverage and finds mutations without testing them.

		Reports are grouped by status and file. Use --json for agents and scripts,
		--statuses to filter findings, and --output to choose a destination independently of format.
		Progress and errors go to stderr. Quality gates always assess the whole run.
	`)
}

func runMutationTests(ctx context.Context) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		options, err := configuredReportOptions()
		if err != nil {
			return execution.NewUsageError(err)
		}
		log.Infoln("Starting...")
		path, _ := os.Getwd()
		if len(args) > 0 {
			path = args[0]
		}
		mod, err := gomodule.Init(path)
		if err != nil {
			return fmt.Errorf("not in a Go module: %w", err)
		}

		output, err := output.Open(cmd.OutOrStdout(), configuration.Get[string](configuration.RunOutputKey))
		if err != nil {
			return err
		}
		defer func() { _ = output.Close() }()
		options.Color = log.ColorEnabled(output.Writer)

		workDir, err := os.MkdirTemp(os.TempDir(), "imp-")
		if err != nil {
			return fmt.Errorf("impossible to create the workdir: %w", err)
		}
		defer cleanUp(workDir)

		results, err := run(ctx, mod, workDir)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		reportErr := report.Write(output, results, options)
		var gate *execution.ExitError
		if reportErr != nil && !errors.As(reportErr, &gate) {
			return reportErr
		}

		if err := output.Commit(); err != nil {
			return err
		}

		for _, target := range results.Replay {
			if target.Resolution != "matched" {
				return fmt.Errorf("exact replay unresolved; no mutants executed (see replay diagnostics)")
			}
		}

		return reportErr
	}
}

func cleanUp(wd string) {
	if err := os.RemoveAll(wd); err != nil {
		log.Errorf("impossible to remove temporary folder: %s\n\t%s", err, wd)
	}
}

func run(ctx context.Context, mod gomodule.GoModule, workDir string) (report.Results, error) {
	fDiff, err := diff.New(mod)
	if err != nil {
		return report.Results{}, err
	}

	ids, err := configuredReplayIDs(mod.Name)
	if err != nil {
		return report.Results{}, execution.NewUsageError(err)
	}

	c := coverage.New(workDir, mod)

	exclude, err := exclusion.New()
	if err != nil {
		return report.Results{}, err
	}

	cProfile, err := c.Run()
	if err != nil {
		return report.Results{}, fmt.Errorf("failed to gather coverage: %w", err)
	}

	wdDealer := workdir.NewCachedDealer(workDir, mod.Root, configuration.Get[[]string](configuration.RunExcludeWorkdir)...)
	defer wdDealer.Clean()

	executor := engine.NewExecutor(mod, wdDealer, cProfile.Elapsed)

	codeData := engine.CodeData{
		Cov:       cProfile.Profile,
		Diff:      fDiff,
		Exclusion: exclude,
		TargetIDs: ids,
	}

	mut := engine.New(mod, codeData, executor)

	return mut.Run(ctx)
}

func setFlagsOnCmd(cmd *cobra.Command) error {
	cmd.Flags().SortFlags = false
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		from := []string{".", "_"}
		to := "-"
		for _, sep := range from {
			name = strings.ReplaceAll(name, sep, to)
		}

		return pflag.NormalizedName(name)
	})

	fls := []*flags.Flag{
		{Name: "mutant-ids", CfgKey: configuration.RunMutantIDsKey, DefaultV: []string{}, Usage: "select exact mutant IDs (comma-separated or repeated)"},
		{Name: "replay-report", CfgKey: configuration.RunReplayReportKey, DefaultV: "", Usage: "select all findings in an identity-aware JSON `FILE`"},
		{Name: "json", CfgKey: configuration.RunJSONKey, DefaultV: false, Usage: "write grouped JSON instead of human-readable text"},
		{Name: "statuses", CfgKey: configuration.RunStatusesKey, DefaultV: "", Usage: "filter findings by comma-separated status `LIST` (default all)"},
		{Name: "verbose", CfgKey: configuration.RunVerboseKey, Shorthand: "v", DefaultV: false, Usage: "expand source context and test commands for all selected statuses"},
		{Name: paramDryRun, CfgKey: configuration.RunDryRunKey, Shorthand: "d", DefaultV: false, Usage: "gather coverage and find mutations without testing them"},
		{Name: paramBuildTags, CfgKey: configuration.RunTagsKey, Shorthand: "t", DefaultV: "", Usage: "comma-separated build tag `LIST`"},
		{Name: paramCoverPackages, CfgKey: configuration.RunCoverPkgKey, DefaultV: "", Usage: "comma-separated coverage package pattern `LIST`"},
		{Name: paramDiff, CfgKey: configuration.RunDiffRef, Shorthand: "D", DefaultV: "", Usage: "limit mutations to changes against Git `REF`"},
		{Name: paramOutput, CfgKey: configuration.RunOutputKey, Shorthand: "o", DefaultV: "", Usage: "write the selected report format to `FILE` (- for stdout)"},
		{Name: paramIntegrationMode, CfgKey: configuration.RunIntegrationMode, Shorthand: "i", DefaultV: false, Usage: "run the whole module's test suite for each mutation"},
		{Name: paramExcludeFiles, CfgKey: configuration.RunExcludeFiles, Shorthand: "E", DefaultV: []string{}, Usage: "exclude matching paths using `REGEXP`; may be repeated"},
		{Name: "include-files", CfgKey: configuration.RunIncludeFiles, DefaultV: []string{}, Usage: "include matching paths using `REGEXP`, overriding generated-file detection; may be repeated"},
		{Name: paramExcludeWorkdir, CfgKey: configuration.RunExcludeWorkdir, DefaultV: []string{}, Usage: "omit a module-relative file or directory from worker copies; may be repeated"},
		{Name: paramThresholdEfficacy, CfgKey: configuration.RunThresholdEfficacyKey, DefaultV: float64(0), Usage: "require test efficacy above `PERCENT` (zero disables)"},
		{Name: paramThresholdMCoverage, CfgKey: configuration.RunThresholdMCoverageKey, DefaultV: float64(0), Usage: "require mutant coverage above `PERCENT` (zero disables)"},
		{Name: paramWorkers, CfgKey: configuration.RunWorkersKey, DefaultV: 0, Usage: "use `N` mutation workers (zero selects automatically)"},
		{Name: paramTestCPU, CfgKey: configuration.RunTestCPUKey, DefaultV: 0, Usage: "set Go test CPU option to `N` (zero leaves it unset)"},
		{Name: paramTimeoutCoefficient, CfgKey: configuration.RunTimeoutCoefficientKey, DefaultV: 0, Usage: "multiply coverage time by `N` for timeouts (zero uses five)"},
	}

	for _, f := range fls {
		err := flags.Set(cmd, f)
		if err != nil {
			return err
		}
	}

	return setMutantTypeFlags(cmd)
}

func setMutantTypeFlags(cmd *cobra.Command) error {
	for _, mt := range mutator.Types {
		name := mt.String()
		usage := fmt.Sprintf("enable %q mutants", name)
		param := strings.ReplaceAll(name, "_", "-")
		param = strings.ToLower(param)
		confKey := configuration.MutantTypeEnabledKey(mt)

		err := flags.Set(cmd, &flags.Flag{
			Name:     param,
			CfgKey:   confKey,
			DefaultV: configuration.IsDefaultEnabled(mt),
			Usage:    usage,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func configuredReplayIDs(module string) ([]string, error) {
	var ids []string
	for _, list := range configuration.Get[[]string](configuration.RunMutantIDsKey) {
		ids = append(ids, strings.Split(list, ",")...)
	}
	if err := report.ValidateMutantIDs(ids); err != nil {
		return nil, err
	}
	if path := configuration.Get[string](configuration.RunReplayReportKey); path != "" {
		// #nosec G304 -- the user explicitly selects this replay input file.
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open replay report: %w", err)
		}
		defer func() { _ = file.Close() }()
		prior, err := report.ReplayIDs(file, module)
		if err != nil {
			return nil, err
		}
		ids = append(ids, prior...)
	}
	slices.Sort(ids)

	return slices.Compact(ids), nil
}
