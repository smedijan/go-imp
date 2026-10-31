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

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

// DefaultTimeoutCoefficient is the default multiplier for the timeout length
// of each test run.
const DefaultTimeoutCoefficient = 5

const evidenceInfrastructure = "infrastructure"

// Internal stop signals must not turn lifecycle errors into CLI interrupts.
var errExecutionStopped = errors.New("mutant execution stopped")

// Executor owns scheduling, mutation restoration, and result delivery for a run.
type Executor interface {
	// Execute calls completed serially for each successfully completed mutant.
	// A nil callback is allowed. On failure or cancellation, it stops scheduling
	// and waits for active attempts to restore their files before returning.
	Execute(ctx context.Context, mutants []mutator.Mutator, completed func(mutator.Mutator)) ([]mutator.Mutator, error)
}

// MutantExecutor runs mutants in isolated, reusable worker directories.
type MutantExecutor struct {
	wdDealer             workdir.Dealer
	execContext          execContext
	mod                  gomodule.GoModule
	buildTags            string
	testExecutionTime    time.Duration
	dryRun               bool
	integrationMode      bool
	testCPU              int
	workers              int
	selectedTest         string
	verificationSnapshot bool
}

// ExecutorOption configures a MutantExecutor.
type ExecutorOption func(MutantExecutor) MutantExecutor

// WithExecContext overrides the default exec.CommandContext with a command adapter.
func WithExecContext(c execContext) ExecutorOption {
	return func(m MutantExecutor) MutantExecutor {
		m.execContext = c

		return m
	}
}

// NewExecutor initializes mutant execution using the configured worker count.
func NewExecutor(mod gomodule.GoModule, wdd workdir.Dealer, elapsed time.Duration, opts ...ExecutorOption) *MutantExecutor {
	buildTags := configuration.Get[string](configuration.RunTagsKey)
	dryRun := configuration.Get[bool](configuration.RunDryRunKey)
	integrationMode := configuration.Get[bool](configuration.RunIntegrationMode)
	testCPU := configuration.Get[int](configuration.RunTestCPUKey)
	tCoefficient := configuration.Get[int](configuration.RunTimeoutCoefficientKey)

	coefficient := DefaultTimeoutCoefficient
	if tCoefficient != 0 {
		coefficient = tCoefficient
	}

	if testCPU != 0 && integrationMode {
		testCPU /= testCPU
	}

	// Use a minimum of 1 second for timeout calculation to prevent
	// unreasonably short timeouts when coverage runs very quickly
	baseTime := elapsed
	if baseTime < time.Second {
		baseTime = time.Second
	}

	workers := configuration.Get[int](configuration.RunWorkersKey)
	if workers == 0 {
		workers = runtime.NumCPU()
	}
	if integrationMode && workers > 1 {
		workers /= 2
	}

	jd := MutantExecutor{
		mod:               mod,
		wdDealer:          wdd,
		buildTags:         buildTags,
		dryRun:            dryRun,
		integrationMode:   integrationMode,
		testCPU:           testCPU,
		testExecutionTime: baseTime * time.Duration(coefficient),
		execContext:       exec.CommandContext,
		workers:           workers,
	}

	for _, opt := range opts {
		jd = opt(jd)
	}

	return &jd
}

// Execute schedules mutants and serializes completion notifications. Returned
// results contain only completed attempts; callers must not report a failed run
// as successful. Within a run, a worker directory is never reused after a
// lifecycle failure.
func (m *MutantExecutor) Execute(ctx context.Context, mutants []mutator.Mutator, completed func(mutator.Mutator)) ([]mutator.Mutator, error) {
	if m.workers < 1 {
		return nil, fmt.Errorf("mutation worker count must be positive")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type outcome struct {
		mutant mutator.Mutator
		err    error
	}
	out := make(chan outcome)
	var scheduling sync.Mutex
	var next int
	var workers sync.WaitGroup
	for id := 0; id < min(m.workers, len(mutants)); id++ {
		workers.Go(func() {
			for {
				scheduling.Lock()
				if runCtx.Err() != nil || next == len(mutants) {
					scheduling.Unlock()

					return
				}
				mutant := mutants[next]
				next++
				scheduling.Unlock()

				attempt := mutantExecutor{executor: m, mutant: mutant}
				result, err := attempt.execute(runCtx, fmt.Sprintf("mutator-%d", id), cancel)
				if err != nil {
					cancel()
				}
				out <- outcome{mutant: result, err: err}
			}
		})
	}
	go func() {
		workers.Wait()
		close(out)
	}()

	var results []mutator.Mutator
	var runErr error
	for result := range out {
		if errors.Is(result.err, errExecutionStopped) {
			continue
		}
		if result.err != nil {
			runErr = errors.Join(runErr, result.err)

			continue
		}
		results = append(results, result.mutant)
		if completed != nil {
			completed(result.mutant)
		}
	}

	return results, errors.Join(runErr, ctx.Err())
}

type execContext = func(ctx context.Context, name string, args ...string) *exec.Cmd

type mutantExecutor struct {
	executor *MutantExecutor
	mutant   mutator.Mutator
}

// execute returns no mutant for an interrupted attempt. Cancellation is reported
// by Execute using the caller's context, never the internal stop signal.
func (m *mutantExecutor) execute(ctx context.Context, workerName string, cancel context.CancelFunc) (mutator.Mutator, error) {
	if ctx.Err() != nil {
		return nil, errExecutionStopped
	}
	observeExecutionMode(m.mutant, m.executor.dryRun)
	if m.mutant.Status() == mutator.NotCovered || m.mutant.Status() == mutator.Skipped || m.executor.dryRun {
		return m.mutant, nil
	}
	rootDir, err := m.executor.wdDealer.Get(workerName)
	if err != nil {
		return nil, fmt.Errorf("get working directory for worker %s: %w", workerName, err)
	}
	m.mutant.SetWorkdir(filepath.Join(rootDir, m.executor.mod.CallingDir))
	if ctx.Err() != nil {
		return nil, errExecutionStopped
	}

	if err := m.mutant.Apply(); err != nil {
		// Stop other workers before restoration, which may itself be slow.
		cancel()

		return nil, errors.Join(fmt.Errorf("apply mutation at %s: %w", m.mutant.Position(), err), m.rollback(cancel))
	}

	status := m.runTests(ctx, rootDir, m.mutant.Pkg())
	if err := m.rollback(cancel); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, errExecutionStopped
	}
	m.mutant.SetStatus(status)

	return m.mutant, nil
}

func (m *mutantExecutor) rollback(cancel context.CancelFunc) error {
	if err := m.mutant.Rollback(); err != nil {
		cancel()

		return fmt.Errorf("restore mutation at %s: %w", m.mutant.Position(), err)
	}

	return nil
}

func (m *mutantExecutor) runTests(parent context.Context, rootDir, pkg string) mutator.Status {
	ctx, cancel := context.WithTimeout(parent, m.executor.testExecutionTime)
	defer cancel()

	args := m.getTestArgs(pkg)
	if recorder, ok := m.mutant.(interface{ SetTestCommand([]string) }); ok {
		recorder.SetTestCommand(append([]string{"go"}, args...))
	}
	cmd := m.executor.execContext(ctx, "go", args...)
	cmd.Dir = m.mutant.Workdir()
	if m.executor.integrationMode {
		cmd.Dir = rootDir
	}
	env := os.Environ()
	if m.executor.verificationSnapshot {
		env = verificationEnvironment()
	}
	cmd.Env = append(cmd.Env, env...)
	if m.executor.wdDealer != nil {
		cmd.Env = append(cmd.Env, fmt.Sprintf("GOTMPDIR=%s", m.executor.wdDealer.WorkDir()))
	}

	// Set up process group for killing entire process tree
	setupProcessGroup(cmd)

	var stdout, stderr synchronizedBuffer
	reader, writer := io.Pipe()
	observed := make(chan testOutput, 1)
	go func() {
		observed <- collectSelectedTestOutput(reader, m.executor.selectedTest)
		_ = reader.Close()
	}()
	cmd.Stdout = io.MultiWriter(&stdout, writer)
	cmd.Stderr = &stderr
	started := time.Now()
	err := run(ctx, cmd)
	_ = writer.Close()
	parsed := <-observed
	evidence := &mutator.ExecutionEvidence{
		Args: append([]string{"go"}, args...), WorkingDirectory: cmd.Dir,
		PackageScope: args[len(args)-1], DurationSeconds: time.Since(started).Seconds(),
		TimeoutSeconds: m.executor.testExecutionTime.Seconds(),
		FailingTests:   parsed.failing, FailingTestsTruncated: parsed.truncated,
		Stdout: stdout.diagnostic(), Stderr: stderr.diagnostic(), JSONValid: parsed.valid,
	}
	if m.executor.verificationSnapshot {
		evidence.PassingTests = parsed.passing
		evidence.PassingTestsTruncated = parsed.passingTruncated
		evidence.SelectedPassingTest = parsed.selectedPassing
	}
	if err != nil {
		var failure synchronizedBuffer
		_, _ = failure.Write([]byte(err.Error()))
		diagnostic := failure.diagnostic()
		evidence.ProcessError = &diagnostic
	}
	status := mutator.NotViable
	var exitErr *exec.ExitError
	switch {
	case parent.Err() != nil:
		status, evidence.Category = mutator.Runnable, evidenceInfrastructure
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, evidence.Category = mutator.TimedOut, "timeout"
	case err == nil:
		status, evidence.Category = mutator.Lived, "success"
		code := 0
		evidence.ExitCode = &code
	case errors.As(err, &exitErr):
		code := exitErr.ExitCode()
		evidence.ExitCode = &code
		status = parsed.status(code)
		switch {
		case status == mutator.Killed:
			evidence.Category = "test"
		case code == 1 && parsed.valid:
			evidence.Category = "build-setup"
		default:
			evidence.Category = evidenceInfrastructure
		}
	default:
		if cmd.Process == nil {
			evidence.Category = "launch"
		} else {
			evidence.Category = evidenceInfrastructure
		}
	}
	if recorder, ok := m.mutant.(interface {
		SetExecutionEvidence(*mutator.ExecutionEvidence)
	}); ok {
		recorder.SetExecutionEvidence(evidence)
	}

	return status
}

func (m *mutantExecutor) getTestArgs(pkg string) []string {
	args := []string{"test", "-json", "-count=1"}
	if m.executor.buildTags != "" {
		args = append(args, "-tags", m.executor.buildTags)
	}
	// Here we add some seconds to the timeout to be sure it's imp that catches the test
	// timeout and not the test itself. The timeout on the test prevents the test.* processes
	// from hanging forever.
	args = append(args, "-timeout", (2*time.Second + m.executor.testExecutionTime).String())
	args = append(args, "-failfast")
	if m.executor.selectedTest != "" {
		args = append(args, "-run", "^"+regexp.QuoteMeta(m.executor.selectedTest)+"$")
	}

	if m.executor.testCPU != 0 {
		args = append(args, "-cpu", fmt.Sprintf("%d", m.executor.testCPU))
	}

	path := pkg
	if m.executor.integrationMode {
		path = "./..."
	}
	args = append(args, path)

	return args
}

func run(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}

	// Ensure cleanup happens regardless of how we exit
	defer func() {
		if cmd.Process != nil {
			// Always kill the process group to catch any child processes
			// This is safe even if the process already exited
			_ = killProcessGroup(cmd)
			// Release OS resources
			_ = cmd.Process.Release()
		}
	}()

	// Monitor context cancellation in parallel with process execution
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Context cancelled/timed out - kill the entire process group
		// Do this BEFORE the parent process exits to catch children
		_ = killProcessGroup(cmd)
		// Wait for the process to actually exit
		<-done

		return ctx.Err()
	case err := <-done:
		// Process completed normally
		return err
	}
}
