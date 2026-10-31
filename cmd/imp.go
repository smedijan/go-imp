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

// Package cmd implements the imp command-line interface.
package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/MakeNowJust/heredoc"
	"github.com/spf13/cobra"

	"github.com/smedijan/go-imp/cmd/internal/flags"
	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/execution"
	"github.com/smedijan/go-imp/internal/report"
)

const paramConfigFile = "config"

// Execute initialises a new Cobra root command (imp) with a custom version
// string used in the --version output.
func Execute(ctx context.Context, version string) error {
	rootCmd, err := newRootCmd(ctx, version)
	if err != nil {
		return err
	}

	return rootCmd.execute()
}

type rootCommand struct {
	cmd *cobra.Command
}

func (gc rootCommand) execute() error {
	command, err := gc.cmd.ExecuteC()
	if err != nil && command != nil && command.Name() == commandName && command.Annotations["report-stage"] != "committed" {
		jsonFlag, _ := command.Flags().GetBool("json")
		var gate *execution.ExitError
		if (jsonFlag || configuration.Get[bool](configuration.RunJSONKey)) && !errors.As(err, &gate) {
			stage := command.Annotations["report-stage"]
			if stage == "" {
				stage = "validation"
			}
			// Keep the original failure as the return value; stderr may itself be broken.
			_ = report.WriteFailure(command.ErrOrStderr(), stage, errors.Is(err, context.Canceled))
		}
	}
	if err != nil && (command == nil || command == gc.cmd) {
		return execution.NewUsageError(err)
	}

	return err
}

func newRootCmd(ctx context.Context, version string) (*rootCommand, error) {
	if version == "" {
		return nil, errors.New("expected a version string")
	}

	cmd := &cobra.Command{
		SilenceUsage:  true,
		SilenceErrors: true,
		Use:           "imp",
		Short:         shortExplainer(),
		Version:       version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString(paramConfigFile)
			if err := configuration.Init([]string{path}); err != nil {
				return execution.NewUsageError(fmt.Errorf("load configuration: %w", err))
			}

			return nil
		},
	}

	run, err := newRunCmd(ctx)
	if err != nil {
		return nil, err

	}
	cmd.AddCommand(run.cmd)

	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return execution.NewUsageError(err)
	})
	cmd.Flags().Bool("version", false, "print version")
	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.PersistentFlags().String(paramConfigFile, "", "read configuration from `FILE`")
	for _, flag := range []*flags.Flag{
		{Name: "quiet", CfgKey: configuration.ImpQuietKey, Shorthand: "q", DefaultV: false, Usage: "suppress progress, not the report or errors"},
		{Name: "no-color", CfgKey: configuration.ImpNoColorKey, DefaultV: false, Usage: "disable colored output"},
	} {
		if err := flags.SetPersistent(cmd, flag); err != nil {
			return nil, err
		}
	}

	return &rootCommand{
		cmd: cmd,
	}, nil
}

func shortExplainer() string {
	return heredoc.Doc(`
		imp is an independent mutation-testing tool for Go projects.
	`)
}
