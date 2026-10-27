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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/engine/workerpool"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestRepeatedCoverageRunsAndTimeoutDerivation(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	root := t.TempDir()
	writeFixture := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture("go.mod", `module example.com

go 1.21
`)
	writeFixture("fixture.go", `package fixture

func Value() int { return 1 }
`)
	writeFixture("fixture_test.go", `package fixture

import "testing"

func TestValue(t *testing.T) {
	t.Log("EXECUTED_TEST_MARKER")
	if Value() != 1 {
		t.Fatal("unexpected value")
	}
}
`)

	mod := gomodule.GoModule{Name: "example.com", Root: root, CallingDir: "."}
	cov := coverage.New(root, mod)
	result, err := cov.Run()
	if err != nil {
		t.Fatalf("first coverage run: %v", err)
	}
	if _, err := cov.Run(); err != nil {
		t.Fatalf("second coverage run: %v", err)
	}

	// Verbose Go test output distinguishes a real execution from a cache hit;
	// count=1 must report the test body each time and never mark the result cached.
	for i := 0; i < 2; i++ {
		cmd := exec.Command("go", "test", "-v", "-count=1", "./...")
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go test: %v\n%s", err, output)
		}
		if !strings.Contains(string(output), "EXECUTED_TEST_MARKER") || strings.Contains(string(output), "(cached)") {
			t.Fatalf("test was not observably executed: %s", output)
		}
	}

	viperSet(map[string]any{configuration.RunTimeoutCoefficientKey: 1})
	defer viperReset()
	wd := newWdDealerStub(t)
	var invoked []string
	dealer := engine.NewExecutorDealer(mod, wd, result.Elapsed, engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		invoked = append([]string{name}, args...)

		return exec.CommandContext(ctx, "go", "version")
	}))
	m := &recordingMutant{mutantStub: &mutantStub{status: mutator.Runnable, pkg: "example.com/fixture"}}
	out := make(chan mutator.Mutator, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	dealer.NewExecutor(m, out, &wg).Start(&workerpool.Worker{Name: "test", ID: 1})
	wg.Wait()
	if got := <-out; got.Status() != mutator.Lived {
		t.Fatalf("status=%s", got.Status())
	}
	baseElapsed := result.Elapsed
	if baseElapsed < time.Second {
		baseElapsed = time.Second
	}
	wantTimeout := (2*time.Second + baseElapsed).String()
	if !strings.Contains(strings.Join(invoked, " "), "-timeout "+wantTimeout) {
		t.Fatalf("mutant command does not derive timeout from coverage elapsed %s: %v", result.Elapsed, invoked)
	}
}

func TestRecordedTestCommand(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tags        string
		elapsed     time.Duration
		cpu         int
		integration bool
		want        []string
	}{
		{name: "no tags", elapsed: 10 * time.Second, want: []string{"test", "-count=1", "-timeout", "12s", "-failfast", "example.com/pkg"}},
		{name: "tags", tags: "tag1,tag2", elapsed: 10 * time.Second, want: []string{"test", "-count=1", "-tags", "tag1,tag2", "-timeout", "12s", "-failfast", "example.com/pkg"}},
		{name: "timeout", elapsed: 30 * time.Second, want: []string{"test", "-count=1", "-timeout", "32s", "-failfast", "example.com/pkg"}},
		{name: "minimum timeout", want: []string{"test", "-count=1", "-timeout", "3s", "-failfast", "example.com/pkg"}},
		{name: "CPU", elapsed: 10 * time.Second, cpu: 4, want: []string{"test", "-count=1", "-timeout", "12s", "-failfast", "-cpu", "4", "example.com/pkg"}},
		{name: "integration", elapsed: 10 * time.Second, integration: true, want: []string{"test", "-count=1", "-timeout", "12s", "-failfast", "./..."}},
		{name: "all flags", tags: "integration", elapsed: 10 * time.Second, cpu: 2, integration: true, want: []string{"test", "-count=1", "-tags", "integration", "-timeout", "12s", "-failfast", "-cpu", "1", "./..."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{
				configuration.RunTagsKey:               tc.tags,
				configuration.RunTestCPUKey:            tc.cpu,
				configuration.RunIntegrationMode:       tc.integration,
				configuration.RunTimeoutCoefficientKey: 1,
			})
			defer viperReset()
			wd := newWdDealerStub(t)
			mod := gomodule.GoModule{Name: "example.com", Root: ".", CallingDir: "."}
			var invoked []string
			dealer := engine.NewExecutorDealer(mod, wd, tc.elapsed, engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
				invoked = append([]string{name}, args...)

				return exec.CommandContext(ctx, "go", "version")
			}))
			m := &recordingMutant{mutantStub: &mutantStub{status: mutator.Runnable, pkg: "example.com/pkg"}}
			out := make(chan mutator.Mutator, 1)
			var wg sync.WaitGroup
			wg.Add(1)
			dealer.NewExecutor(m, out, &wg).Start(&workerpool.Worker{Name: "test", ID: 1})
			wg.Wait()
			if got := <-out; got.Status() != mutator.Lived {
				t.Fatalf("status=%s", got.Status())
			}
			want := append([]string{"go"}, tc.want...)
			if delta := cmp.Diff(want, invoked); delta != "" {
				t.Errorf("invoked command (-want +got):\n%s", delta)
			}
			if delta := cmp.Diff(want, m.command); delta != "" {
				t.Errorf("recorded command (-want +got):\n%s", delta)
			}
		})
	}
}

type recordingMutant struct {
	*mutantStub
	command []string
}

func (m *recordingMutant) SetTestCommand(command []string) {
	m.command = append([]string(nil), command...)
}
