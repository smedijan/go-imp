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

package engine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

const expectedTimeout = 10 * time.Second

type execContext = func(ctx context.Context, name string, args ...string) *exec.Cmd

func TestMutatorTestExecution(t *testing.T) {
	for _, tc := range []struct {
		testResult execContext
		name       string
		initial    mutator.Status
		want       mutator.Status
	}{
		{name: "not covered", initial: mutator.NotCovered, want: mutator.NotCovered},
		{name: "skipped", initial: mutator.Skipped, want: mutator.Skipped},
		{name: "tests pass", testResult: fakeExecCommandSuccess, initial: mutator.Runnable, want: mutator.Lived},
		{name: "tests fail", testResult: fakeExecCommandTestsFailure, initial: mutator.Runnable, want: mutator.Killed},
		{name: "build fails", testResult: fakeExecCommandBuildFailure, initial: mutator.Runnable, want: mutator.NotViable},
		{name: "cannot start", testResult: fakeExecCommandStartupFailure, initial: mutator.Runnable, want: mutator.NotViable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunDryRunKey: false})
			defer viperReset()
			wd := newWdDealerStub(t)
			var command *exec.Cmd
			mod := gomodule.GoModule{Name: "example.com", Root: ".", CallingDir: "."}
			executor := engine.NewExecutor(mod, wd, expectedTimeout, engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
				if tc.testResult == nil {
					t.Error("unrunnable mutant launched a command")

					return fakeExecCommandSuccess(ctx, name, args...)
				}
				command = tc.testResult(ctx, name, args...)

				return command
			}))
			mut := &mutantStub{status: tc.initial, pkg: "example.com"}
			got := executeMutants(t, executor, mut)[0]
			if got.Status() != tc.want {
				t.Fatalf("status = %s, want %s", got.Status(), tc.want)
			}
			runnable := tc.initial == mutator.Runnable
			if mut.applyCalled != runnable || mut.rollbackCalled != runnable {
				t.Fatalf("apply=%t rollback=%t, runnable=%t", mut.applyCalled, mut.rollbackCalled, runnable)
			}
			if runnable && !slices.Contains(command.Env, "GOTMPDIR="+wd.WorkDir()) {
				t.Fatalf("missing worker temporary directory in environment: %v", command.Env)
			}
		})
	}
}

func TestMutatorClassifiesRealGoTestFailures(t *testing.T) {
	testCases := []struct {
		name        string
		source      string
		testSource  string
		extraFiles  map[string]string
		integration bool
		want        mutator.Status
	}{
		{
			name:       "compile failure",
			source:     "package fixture\nfunc broken() { missingSymbol() }\n",
			testSource: "package fixture\nimport \"testing\"\nfunc TestPass(t *testing.T) {}\n",
			want:       mutator.NotViable,
		},
		{
			name:       "assertion failure",
			source:     "package fixture\n",
			testSource: "package fixture\nimport \"testing\"\nfunc TestFail(t *testing.T) { t.Fatal(\"assertion failed\") }\n",
			want:       mutator.Killed,
		},
		{
			name:       "test panic",
			source:     "package fixture\n",
			testSource: "package fixture\nimport \"testing\"\nfunc TestPanic(t *testing.T) { panic(\"test panic\") }\n",
			want:       mutator.Killed,
		},
		{
			name:       "TestMain failure after passing tests",
			source:     "package fixture\n",
			testSource: "package fixture\nimport (\"os\"; \"testing\")\nfunc TestPass(t *testing.T) {}\nfunc TestMain(m *testing.M) { _ = m.Run(); os.Exit(1) }\n",
			want:       mutator.NotViable,
		},
		{
			name:        "integration run with compile and test failures",
			integration: true,
			extraFiles: map[string]string{
				"valid/valid.go":      "package valid\n",
				"valid/valid_test.go": "package valid\nimport \"testing\"\nfunc TestFail(t *testing.T) { t.Fatal(\"assertion failed\") }\n",
				"broken/broken.go":    "package broken\nfunc broken() { missingSymbol() }\n",
			},
			want: mutator.NotViable,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{
				configuration.RunDryRunKey:       false,
				configuration.RunIntegrationMode: tc.integration,
			})
			defer viperReset()

			rootDir := t.TempDir()
			wdDealer := fixtureWorkdirDealer{path: rootDir, tempDir: t.TempDir()}
			files := map[string]string{"go.mod": "module example.com/fixture\n\ngo 1.21\n"}
			if tc.source != "" {
				files["fixture.go"] = tc.source
				files["fixture_test.go"] = tc.testSource
			}
			for name, content := range tc.extraFiles {
				files[name] = content
			}
			for name, content := range files {
				path := filepath.Join(rootDir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			mod := gomodule.GoModule{Name: "example.com/fixture", Root: rootDir, CallingDir: "."}
			executor := engine.NewExecutor(mod, wdDealer, expectedTimeout)
			mut := &mutantStub{status: mutator.Runnable, pkg: "."}
			got := executeMutants(t, executor, mut)[0]
			if got.Status() != tc.want {
				t.Errorf("status = %v, want %v", got.Status(), tc.want)
			}
		})
	}
}

type fixtureWorkdirDealer struct {
	path    string
	tempDir string
}

func (d fixtureWorkdirDealer) Get(string) (string, error) { return d.path, nil }
func (fixtureWorkdirDealer) Clean()                       {}
func (d fixtureWorkdirDealer) WorkDir() string            { return d.tempDir }

func TestMutatorCommandDirectoryAndTimeoutCoefficient(t *testing.T) {
	for _, integration := range []bool{false, true} {
		t.Run(fmt.Sprintf("integration=%t", integration), func(t *testing.T) {
			viperSet(map[string]any{
				configuration.RunIntegrationMode:       integration,
				configuration.RunTimeoutCoefficientKey: 4,
			})
			defer viperReset()
			root := t.TempDir()
			callingDir := filepath.Join("test", "dir")
			if err := os.MkdirAll(filepath.Join(root, callingDir), 0o700); err != nil {
				t.Fatal(err)
			}
			mod := gomodule.GoModule{Name: "example.com", Root: root, CallingDir: callingDir}
			wd := fixtureWorkdirDealer{path: root, tempDir: t.TempDir()}
			var command *exec.Cmd
			var invoked []string
			executor := engine.NewExecutor(mod, wd, expectedTimeout, engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
				invoked = args
				command = fakeExecCommandSuccess(ctx, name, args...)

				return command
			}))
			mut := &mutantStub{status: mutator.Runnable, pkg: "example.com/my/package"}
			if got := executeMutants(t, executor, mut)[0]; got.Status() != mutator.Lived {
				t.Fatalf("status = %s", got.Status())
			}
			wantDir := filepath.Join(root, callingDir)
			if integration {
				wantDir = root
			}
			if command.Dir != wantDir || mut.Workdir() != filepath.Join(root, callingDir) {
				t.Fatalf("command dir=%q mutation dir=%q, want %q and %q", command.Dir, mut.Workdir(), wantDir, filepath.Join(root, callingDir))
			}
			if !strings.Contains(strings.Join(invoked, " "), "-timeout 42s") {
				t.Fatalf("timeout coefficient not applied: %v", invoked)
			}
		})
	}
}

func TestCoverageProcessSuccess(_ *testing.T) {
	if os.Getenv("GO_TEST_PROCESS") != "1" {
		return
	}
	os.Exit(0) // skipcq: RVV-A0003
}

func TestProcessTestsFailure(_ *testing.T) {
	if os.Getenv("GO_TEST_PROCESS") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("{\"Action\":\"run\",\"Package\":\"example.com\",\"Test\":\"TestFakeFailure\"}\n{\"Action\":\"fail\",\"Package\":\"example.com\",\"Test\":\"TestFakeFailure\"}\n{\"Action\":\"fail\",\"Package\":\"example.com\"}\n")
	os.Exit(1) // skipcq: RVV-A0003
}

func TestProcessBuildFailure(_ *testing.T) {
	if os.Getenv("GO_TEST_PROCESS") != "1" {
		return
	}
	os.Exit(2) // skipcq: RVV-A0003
}

func fakeExecCommandSuccess(ctx context.Context, command string, args ...string) *exec.Cmd {
	return helperCommand(ctx, "TestCoverageProcessSuccess", command, args...)
}

func fakeExecCommandTestsFailure(ctx context.Context, command string, args ...string) *exec.Cmd {
	return helperCommand(ctx, "TestProcessTestsFailure", command, args...)
}

func fakeExecCommandBuildFailure(ctx context.Context, command string, args ...string) *exec.Cmd {
	return helperCommand(ctx, "TestProcessBuildFailure", command, args...)
}

func fakeExecCommandStartupFailure(ctx context.Context, _ string, _ ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "/does/not/exist")
}

func helperCommand(ctx context.Context, test, command string, args ...string) *exec.Cmd {
	cs := append([]string{"-test.run=^" + test + "$", "--", command}, args...)
	// #nosec G204 G702 -- the test binary is a local command adapter.
	cmd := exec.CommandContext(ctx, os.Args[0], cs...)
	cmd.Env = []string{"GO_TEST_PROCESS=1"}

	return cmd
}
