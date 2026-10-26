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

package cmd_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/execution"
)

func TestRootCommand(t *testing.T) {
	out, err := executeCLI(context.Background(), t, "1.2.3", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"imp", "--config", "--quiet", "--no-color", "--version", "run"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
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
			out, err := executeCLI(context.Background(), t, "test-version", tc.args...)
			if tc.invalid {
				var usage *execution.UsageError
				if !errors.As(err, &usage) || usage.ExitCode() != 2 {
					t.Fatalf("expected usage error, got %v", err)
				}
			} else if err != nil || out != "test-version\n" {
				t.Fatalf("version output %q, error %v", out, err)
			}
		})
	}
}

func TestExecute(t *testing.T) {
	if _, err := executeCLI(context.Background(), t, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := executeCLI(context.Background(), t, ""); err == nil {
		t.Fatal("expected failure without version")
	}
}
