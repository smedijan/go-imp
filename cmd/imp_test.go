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
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/execution"
)

func TestRootCommand(t *testing.T) {
	const boolType = "bool"

	c, err := newRootCmd(context.Background(), "1.2.3")
	if err != nil {
		t.Fatal("newRootCmd should not fail")
	}
	_ = c.execute()
	cmd := c.cmd

	if cmd.Use != "imp" {
		t.Errorf("expected command name %q, got %q", "imp", cmd.Use)
	}

	if cmd.Version != "1.2.3" {
		t.Errorf("expected %q, got %q", "1.2.3", cmd.Version)
	}

	cfgFile := cmd.Flag("config")
	if cfgFile == nil {
		t.Fatal("expected to have a config flag")
	}
	if cfgFile.Value.Type() != "string" {
		t.Errorf("expected value type to be 'string', got %v", cfgFile.Value.Type())
	}
	if cfgFile.DefValue != "" {
		t.Errorf("expected default value to be empty, got %v", cfgFile.DefValue)
	}

	quietFlag := cmd.Flag("quiet")
	if quietFlag == nil {
		t.Fatal("expected a quiet flag")
	}
	if quietFlag.Value.Type() != boolType {
		t.Errorf("expected value type to be 'bool', got %v", quietFlag.Value.Type())
	}
	if quietFlag.DefValue != "false" {
		t.Errorf("expected default value to be false, got %v", quietFlag.DefValue)
	}
}

func TestVersionAndInvalidCommands(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		invalid bool
	}{
		{name: "version", args: []string{"--version"}},
		{name: "unknown command", args: []string{"unknown"}, invalid: true},
		{name: "extra arguments", args: []string{"run", "one", "two"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			root, err := newRootCmd(context.Background(), "test-version")
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			root.cmd.SetOut(&out)
			root.cmd.SetArgs(tc.args)
			err = root.execute()
			if tc.invalid {
				var usage *execution.UsageError
				if !errors.As(err, &usage) || usage.ExitCode() != 2 {
					t.Fatalf("expected usage error, got %v", err)
				}
			} else if err != nil || out.String() != "test-version\n" {
				t.Fatalf("version output %q, error %v", out.String(), err)
			}
		})
	}
}

func TestExecute(t *testing.T) {
	t.Run("should not fail", func(t *testing.T) {
		err := Execute(context.Background(), "1.2.3")
		if err != nil {
			t.Errorf("execute should not fail")
		}
	})

	t.Run("should fail if version is not set", func(t *testing.T) {
		err := Execute(context.Background(), "")
		if err == nil {
			t.Errorf("expected failure")
		}

	})
}
