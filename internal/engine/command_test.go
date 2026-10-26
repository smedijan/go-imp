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
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/engine/workerpool"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestRecordedTestCommand(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tags        string
		elapsed     time.Duration
		cpu         int
		integration bool
		want        []string
	}{
		{name: "no tags", elapsed: 10 * time.Second, want: []string{"test", "-timeout", "12s", "-failfast", "example.com/pkg"}},
		{name: "tags", tags: "tag1,tag2", elapsed: 10 * time.Second, want: []string{"test", "-tags", "tag1,tag2", "-timeout", "12s", "-failfast", "example.com/pkg"}},
		{name: "timeout", elapsed: 30 * time.Second, want: []string{"test", "-timeout", "32s", "-failfast", "example.com/pkg"}},
		{name: "minimum timeout", want: []string{"test", "-timeout", "3s", "-failfast", "example.com/pkg"}},
		{name: "CPU", elapsed: 10 * time.Second, cpu: 4, want: []string{"test", "-timeout", "12s", "-failfast", "-cpu", "4", "example.com/pkg"}},
		{name: "integration", elapsed: 10 * time.Second, integration: true, want: []string{"test", "-timeout", "12s", "-failfast", "./..."}},
		{name: "all flags", tags: "integration", elapsed: 10 * time.Second, cpu: 2, integration: true, want: []string{"test", "-tags", "integration", "-timeout", "12s", "-failfast", "-cpu", "1", "./..."}},
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
