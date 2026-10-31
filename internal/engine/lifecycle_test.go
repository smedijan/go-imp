package engine_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

type lifecycleMutant struct {
	mutator.EvidenceRecorder
	*mutantStub
	apply    func(*lifecycleMutant) error
	rollback func(*lifecycleMutant) error
}

func (m *lifecycleMutant) Apply() error {
	m.applyCalled = true
	if m.apply != nil {
		return m.apply(m)
	}

	return nil
}

func (m *lifecycleMutant) Rollback() error {
	m.rollbackCalled = true
	if m.rollback != nil {
		return m.rollback(m)
	}

	return nil
}

func TestExecutorLifecycleFailureStopsWorkerReuse(t *testing.T) {
	applyErr := errors.New("partial write failure")
	restoreErr := errors.New("restore failure")
	for _, tc := range []struct {
		name    string
		apply   error
		restore error
	}{
		{name: "Apply failed after writing", apply: applyErr},
		{name: "Rollback failed", restore: restoreErr},
		{name: "Apply and Rollback failed", apply: applyErr, restore: restoreErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunWorkersKey: 1})
			defer viperReset()
			root := t.TempDir()
			original := []byte("original source")
			if err := os.WriteFile(filepath.Join(root, "source.go"), original, 0o600); err != nil {
				t.Fatal(err)
			}
			wd := workdir.NewCachedDealer(t.TempDir(), root)
			defer wd.Clean()
			mod := gomodule.GoModule{Name: "example.com", Root: root, CallingDir: "."}
			executor := engine.NewExecutor(mod, wd, expectedTimeout, engine.WithExecContext(fakeExecCommandSuccess))
			first := &mutantStub{status: mutator.Runnable}
			failed := &lifecycleMutant{mutantStub: &mutantStub{status: mutator.Runnable}}
			failed.apply = func(m *lifecycleMutant) error {
				path := filepath.Join(m.Workdir(), "source.go")
				if err := os.WriteFile(path, []byte("partially mutated"), 0o600); err != nil {
					return err
				}

				return tc.apply
			}
			failed.rollback = func(m *lifecycleMutant) error {
				if tc.restore != nil {
					return tc.restore
				}

				return os.WriteFile(filepath.Join(m.Workdir(), "source.go"), original, 0o600)
			}
			pending := &mutantStub{status: mutator.Runnable}
			var notified []mutator.Mutator
			results, err := executor.Execute(context.Background(), []mutator.Mutator{first, failed, pending}, func(m mutator.Mutator) {
				notified = append(notified, m)
			})
			for _, want := range []error{tc.apply, tc.restore} {
				if want != nil && !errors.Is(err, want) {
					t.Errorf("error %v does not preserve %v", err, want)
				}
			}
			if len(results) != 1 || results[0] != first || len(notified) != 1 || notified[0] != first {
				t.Fatalf("completed results=%v notifications=%v", results, notified)
			}
			if !failed.rollbackCalled || pending.applyCalled || pending.rollbackCalled {
				t.Fatalf("failed attempt restored=%t, pending applied=%t restored=%t", failed.rollbackCalled, pending.applyCalled, pending.rollbackCalled)
			}
			if first.Workdir() != failed.Workdir() {
				t.Fatal("successful worker copy was not reused")
			}
			//nolint:gosec // test-owned source and worker-copy paths
			got, readErr := os.ReadFile(filepath.Join(root, "source.go"))
			if readErr != nil || string(got) != string(original) {
				t.Fatalf("original source changed: %q, %v", got, readErr)
			}
			if tc.restore == nil {
				got, readErr = os.ReadFile(filepath.Join(failed.Workdir(), "source.go"))
				if readErr != nil || string(got) != string(original) {
					t.Fatalf("partial Apply was not restored: %q, %v", got, readErr)
				}
			}
		})
	}
}

func TestExecutorWorkdirFailureReachesEngine(t *testing.T) {
	viperSet(map[string]any{configuration.RunWorkersKey: 1})
	defer viperReset()
	failure := errors.New("copy failed")
	wd := &dealerStub{fnGet: func(string) (string, error) { return "", failure }}
	mod := gomodule.GoModule{Name: "example.com", Root: ".", CallingDir: "."}
	executor := engine.NewExecutor(mod, wd, expectedTimeout)
	source := fstest.MapFS{"source.go": {Data: []byte("package example\nfunc Value(a int) int { return a+1 }\n")}}
	profile := coverage.Profile{"source.go": {{StartLine: 2, StartCol: 1, EndLine: 2, EndCol: 100}}}
	e := engine.New(mod, engine.CodeData{Cov: profile}, executor, engine.WithDirFs(source))
	results, err := e.Run(context.Background())
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("expected workdir error, got %v", err)
	}
	if len(results.Mutants) != 0 || results.Module != mod.Name {
		t.Fatalf("unexpected failed-run results: %+v", results)
	}
}

func TestExecutorSkipsWithoutCreatingWorkerCopies(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry-run=%t", dryRun), func(t *testing.T) {
			viperSet(map[string]any{configuration.RunDryRunKey: dryRun})
			defer viperReset()
			wd := &dealerStub{fnGet: func(string) (string, error) {
				t.Error("unrunnable mutant requested a copy")

				return "", errors.New("unexpected copy")
			}}
			executor := engine.NewExecutor(gomodule.GoModule{}, wd, expectedTimeout)
			mutants := []mutator.Mutator{&mutantStub{status: mutator.NotCovered}, &mutantStub{status: mutator.Skipped}}
			if dryRun {
				mutants = append(mutants, &mutantStub{status: mutator.Runnable})
			}
			seen := make(map[mutator.Mutator]bool)
			results, err := executor.Execute(context.Background(), mutants, func(m mutator.Mutator) {
				if seen[m] {
					t.Error("duplicate completion")
				}
				seen[m] = true
			})
			if err != nil || len(results) != len(mutants) || len(seen) != len(mutants) {
				t.Fatalf("results=%d completions=%d error=%v", len(results), len(seen), err)
			}
		})
	}
}

func TestExecutorFailureCancelsActiveCommandAndRestoresFiles(t *testing.T) {
	viperSet(map[string]any{configuration.RunWorkersKey: 2})
	defer viperReset()
	failure := errors.New("Apply failed")
	releaseFailure := make(chan struct{})
	restoring := make(chan struct{})
	releaseRestoration := make(chan struct{})
	var releaseRestorationOnce sync.Once
	defer releaseRestorationOnce.Do(func() { close(releaseRestoration) })
	defer func() {
		select {
		case <-releaseFailure:
		default:
			close(releaseFailure)
		}
	}()
	first := &lifecycleMutant{
		mutantStub: &mutantStub{status: mutator.Runnable},
		apply: func(*lifecycleMutant) error {
			<-releaseFailure

			return failure
		},
		rollback: func(*lifecycleMutant) error {
			close(restoring)
			<-releaseRestoration

			return nil
		},
	}
	active := fileWritingMutant(t)
	activeRestored := make(chan struct{})
	restoreFailure := errors.New("active worker restoration failed")
	restoreActive := active.rollback
	active.rollback = func(m *lifecycleMutant) error {
		err := restoreActive(m)
		close(activeRestored)

		return errors.Join(err, restoreFailure)
	}
	pending := &mutantStub{status: mutator.Runnable}
	marker := filepath.Join(t.TempDir(), "started")
	var command *exec.Cmd
	wd := newWdDealerStub(t)
	mod := gomodule.GoModule{CallingDir: "."}
	executor := engine.NewExecutor(mod, wd, expectedTimeout, engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		command = waitingCommand(ctx, marker, name, args...)

		return command
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := executor.Execute(ctx, []mutator.Mutator{first, active, pending}, nil)
		finished <- err
	}()
	waitForCommand(ctx, t, marker)
	close(releaseFailure)
	select {
	case <-restoring:
	case <-ctx.Done():
		t.Fatal("failed attempt did not begin restoration")
	}
	select {
	case <-activeRestored:
	case <-time.After(3 * time.Second):
		t.Fatal("failure did not cancel active command before restoration completed")
	}
	select {
	case <-finished:
		t.Fatal("execution returned before all restoration completed")
	default:
	}
	releaseRestorationOnce.Do(func() { close(releaseRestoration) })
	select {
	case err := <-finished:
		if !errors.Is(err, failure) || !errors.Is(err, restoreFailure) {
			t.Fatalf("concurrent lifecycle failures were lost: %v", err)
		}
		if errors.Is(err, context.Canceled) {
			t.Fatalf("internal cancellation disguised a lifecycle error as an interrupt: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("active command did not stop")
	}
	if !first.rollbackCalled || pending.applyCalled {
		t.Fatalf("failed attempt restored=%t, pending started=%t", first.rollbackCalled, pending.applyCalled)
	}
	assertRestored(t, active)
	if command.ProcessState == nil {
		t.Fatal("active process was not reaped")
	}
}

func TestExecutorCancellationAndTimeoutRestoreBeforeReturning(t *testing.T) {
	for _, mode := range []string{"cancel", "parent deadline", "mutant timeout"} {
		t.Run(mode, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunWorkersKey: 1, configuration.RunTimeoutCoefficientKey: 1})
			defer viperReset()
			elapsed := expectedTimeout
			parentTimeout := 10 * time.Second
			if mode == "mutant timeout" {
				elapsed = 0 // exercises the one-second minimum
			}
			if mode == "parent deadline" {
				parentTimeout = 2 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), parentTimeout)
			defer cancel()
			marker := filepath.Join(t.TempDir(), "started")
			var command *exec.Cmd
			executor := engine.NewExecutor(gomodule.GoModule{CallingDir: "."}, newWdDealerStub(t), elapsed,
				engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
					command = waitingCommand(ctx, marker, name, args...)

					return command
				}))
			active := fileWritingMutant(t)
			type outcome struct {
				results []mutator.Mutator
				err     error
			}
			finished := make(chan outcome, 1)
			go func() {
				results, err := executor.Execute(ctx, []mutator.Mutator{active}, nil)
				finished <- outcome{results: results, err: err}
			}()
			waitForCommand(ctx, t, marker)
			if mode == "cancel" {
				cancel()
			}
			select {
			case got := <-finished:
				switch mode {
				case "cancel":
					if !errors.Is(got.err, context.Canceled) || len(got.results) != 0 {
						t.Fatalf("expected canceled run, got %+v", got)
					}
				case "parent deadline":
					if !errors.Is(got.err, context.DeadlineExceeded) || len(got.results) != 0 {
						t.Fatalf("expected run deadline, got %+v", got)
					}
				case "mutant timeout":
					if got.err != nil || len(got.results) != 1 || got.results[0].Status() != mutator.TimedOut {
						t.Fatalf("expected timed-out mutant, got %+v", got)
					}
				}
			case <-time.After(15 * time.Second):
				t.Fatal("command cancellation hung")
			}
			evidence := active.ExecutionEvidence()
			category := "infrastructure"
			if mode == "mutant timeout" {
				category = "timeout"
			}
			if evidence == nil || evidence.Category != category || evidence.DurationSeconds <= 0 {
				t.Fatalf("missing timeout/cancellation evidence: %+v", evidence)
			}
			if mode == "mutant timeout" && evidence.TimeoutSeconds != 1 {
				t.Fatalf("incorrect minimum timeout: %+v", evidence)
			}
			assertRestored(t, active)
			if command.ProcessState == nil {
				t.Fatal("process was not reaped")
			}
		})
	}
}

func fileWritingMutant(t *testing.T) *lifecycleMutant {
	t.Helper()

	return &lifecycleMutant{
		mutantStub: &mutantStub{status: mutator.Runnable},
		apply: func(m *lifecycleMutant) error {
			return os.WriteFile(filepath.Join(m.Workdir(), "source.go"), []byte("mutated"), 0o600)
		},
		rollback: func(m *lifecycleMutant) error {
			return os.WriteFile(filepath.Join(m.Workdir(), "source.go"), []byte("restored"), 0o600)
		},
	}
}

func assertRestored(t *testing.T, m *lifecycleMutant) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(m.Workdir(), "source.go"))
	if err != nil || string(got) != "restored" || !m.rollbackCalled {
		t.Fatalf("mutation not restored before return: %q, %v", got, err)
	}
}

func waitingCommand(ctx context.Context, marker, command string, args ...string) *exec.Cmd {
	cmd := helperCommand(ctx, "TestProcessWaitForCancellation", command, args...)
	cmd.Env = append(cmd.Env, "GO_TEST_WAIT_FILE="+marker)

	return cmd
}

func TestProcessWaitForCancellation(t *testing.T) {
	if os.Getenv("GO_TEST_PROCESS") != "1" {
		return
	}
	//nolint:gosec // parent supplies a test-owned readiness file
	if err := os.WriteFile(os.Getenv("GO_TEST_WAIT_FILE"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}

func waitForCommand(ctx context.Context, t *testing.T, marker string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("command did not become ready")
		case <-ticker.C:
		}
	}
}

func TestExecutorWorkerLimits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		configured  int
		integration bool
		want        int
	}{
		{name: "automatic", want: runtime.NumCPU()},
		{name: "automatic integration", integration: true, want: max(1, runtime.NumCPU()/2)},
		{name: "explicit", configured: 3, want: 3},
		{name: "integration", configured: 4, integration: true, want: 2},
		{name: "single integration worker", configured: 1, integration: true, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunWorkersKey: tc.configured, configuration.RunIntegrationMode: tc.integration})
			defer viperReset()
			root := t.TempDir()
			var mu sync.Mutex
			identities := make(map[string]bool)
			wd := &dealerStub{fnGet: func(id string) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				identities[id] = true

				return root, nil
			}}
			started := make(chan struct{}, tc.want+1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var mutants []mutator.Mutator
			for i := 0; i < tc.want+1; i++ {
				mutants = append(mutants, &lifecycleMutant{
					mutantStub: &mutantStub{status: mutator.Runnable},
					apply: func(*lifecycleMutant) error {
						started <- struct{}{}
						<-release

						return nil
					},
				})
			}
			executor := engine.NewExecutor(gomodule.GoModule{CallingDir: "."}, wd, expectedTimeout, engine.WithExecContext(fakeExecCommandStartupFailure))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				results, err := executor.Execute(ctx, mutants, nil)
				if err == nil && len(results) != len(mutants) {
					err = fmt.Errorf("got %d results, want %d", len(results), len(mutants))
				}
				finished <- err
			}()
			for i := 0; i < tc.want; i++ {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("configured workers did not start")
				}
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("workers did not finish")
			}
			if len(identities) != tc.want {
				t.Fatalf("used %d worker identities, want %d", len(identities), tc.want)
			}
		})
	}
}

func TestExecutorEmptyCanceledAndInvalidRuns(t *testing.T) {
	viperSet(map[string]any{configuration.RunWorkersKey: 1})
	defer viperReset()
	executor := engine.NewExecutor(gomodule.GoModule{}, newWdDealerStub(t), expectedTimeout)
	if results, err := executor.Execute(context.Background(), nil, nil); err != nil || len(results) != 0 {
		t.Fatalf("empty run: %v, %v", results, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mut := &mutantStub{status: mutator.Runnable}
	if results, err := executor.Execute(ctx, []mutator.Mutator{mut}, nil); !errors.Is(err, context.Canceled) || len(results) != 0 || mut.applyCalled {
		t.Fatalf("pre-canceled run: %v, %v", results, err)
	}
	configuration.Set(configuration.RunWorkersKey, -1)
	executor = engine.NewExecutor(gomodule.GoModule{}, newWdDealerStub(t), expectedTimeout)
	if _, err := executor.Execute(context.Background(), []mutator.Mutator{mut}, nil); err == nil {
		t.Fatal("invalid worker count was accepted")
	}
}
