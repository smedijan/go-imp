package engine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

type confirmationMutant struct {
	*evidenceMutant
	applies, rollbacks int
}

func (m *confirmationMutant) Apply() error {
	m.applies++

	return os.WriteFile(filepath.Join(m.Workdir(), "state"), []byte("mutant"), 0600)
}
func (m *confirmationMutant) Rollback() error {
	m.rollbacks++

	return os.WriteFile(filepath.Join(m.Workdir(), "state"), []byte("pristine"), 0600)
}

type confirmationCase struct {
	name                                  string
	outcomes                              []string
	confidence                            string
	want                                  mutator.Status
	enabled, cancelBaseline, cancelRepeat bool
	contaminated                          bool
}

func TestExecutorConfirmation(t *testing.T) {
	for _, tc := range []confirmationCase{
		{name: "default unchanged", outcomes: []string{"fail"}, want: mutator.Killed},
		{name: "passing not repeated", enabled: true, outcomes: []string{"pass"}, want: mutator.Lived},
		{name: "confirmed causality", enabled: true, outcomes: []string{"fail", "pass", "fail"}, confidence: "confirmed", want: mutator.Killed},
		{name: "baseline failure", enabled: true, outcomes: []string{"fail", "fail"}, confidence: "baseline-unstable", want: mutator.NotViable},
		{name: "contaminated worker recovery", enabled: true, contaminated: true, outcomes: []string{"contamination", "contamination", "contamination"}, confidence: "inconsistent", want: mutator.NotViable},
		{name: "alternating repeat", enabled: true, outcomes: []string{"fail", "pass", "pass"}, confidence: "inconsistent", want: mutator.NotViable},
		{name: "clean recovery after timeout", enabled: true, outcomes: []string{"timeout", "pass", "pass"}, confidence: "inconsistent", want: mutator.TimedOut},
		{name: "baseline timeout", enabled: true, outcomes: []string{"fail", "timeout"}, confidence: "unresolved-timeout", want: mutator.NotViable},
		{name: "repeat timeout", enabled: true, outcomes: []string{"fail", "pass", "timeout"}, confidence: "unresolved-timeout", want: mutator.NotViable},
		{name: "baseline infrastructure", enabled: true, outcomes: []string{"fail", "infra"}, confidence: "infrastructure", want: mutator.NotViable},
		{name: "initial infrastructure not retried", enabled: true, outcomes: []string{"infra"}, confidence: "infrastructure", want: mutator.NotViable},
		{name: "cancel repeat", enabled: true, outcomes: []string{"fail", "pass", "pass"}, cancelRepeat: true},
		{name: "cancel baseline", enabled: true, outcomes: []string{"fail", "pass"}, cancelBaseline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunConfirmOutcomesKey: tc.enabled, configuration.RunWorkersKey: 1, configuration.RunTimeoutCoefficientKey: 1})
			defer viperReset()
			t.Setenv("GORACE", "atexit_sleep_ms=0")
			src, tmp := t.TempDir(), t.TempDir()
			for name, content := range map[string]string{"state": "pristine", ".git": "gitdir: /never/follow/this"} {
				if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			dealer := workdir.NewCachedDealer(tmp, src)
			defer dealer.Clean()
			if tc.contaminated {
				worker, err := dealer.Get("mutator-0")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(worker, "contaminated"), []byte("previous-test"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var commands []*exec.Cmd
			var selections [][]string
			adapter := func(ctx context.Context, name string, args ...string) *exec.Cmd {
				index := len(commands)
				if index >= len(tc.outcomes) {
					t.Fatal("attempt bound exceeded")
				}
				cmd := helperCommand(ctx, "TestConfirmationProcess", name, args...)
				cmd.Env = append(cmd.Env, "CONFIRM_RESULT="+tc.outcomes[index], fmt.Sprintf("CONFIRM_INDEX=%d", index))
				commands = append(commands, cmd)
				selections = append(selections, append([]string(nil), args...))
				if index == 0 {
					// The live checkout changes after snapshot capture; confirmation must
					// retain the original source rather than copy the later checkout.
					if err := os.WriteFile(filepath.Join(src, "state"), []byte("later-edit"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if (index == 1 && tc.cancelBaseline) || (index == 2 && tc.cancelRepeat) {
					cancel()
				}

				return cmd
			}
			executor := engine.NewExecutor(gomodule.GoModule{Root: src, CallingDir: "."}, dealer, time.Millisecond, engine.WithExecContext(adapter))
			m := &confirmationMutant{evidenceMutant: &evidenceMutant{mutantStub: &mutantStub{status: mutator.Runnable, pkg: "example.test"}}}
			started := time.Now()
			results, err := executor.Execute(ctx, []mutator.Mutator{m}, nil)
			if tc.cancelBaseline || tc.cancelRepeat {
				if err == nil || len(results) != 0 || m.applies != m.rollbacks {
					t.Fatalf("cancel: results=%v err=%v apply=%d rollback=%d", results, err, m.applies, m.rollbacks)
				}

				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertConfirmation(t, tc, m, commands, selections, started)
		})
	}
}

func assertConfirmation(t *testing.T, tc confirmationCase, m *confirmationMutant, commands []*exec.Cmd, selections [][]string, started time.Time) {
	t.Helper()
	e := m.ExecutionEvidence()
	if m.Status() != tc.want || e.Confidence != tc.confidence || len(commands) != len(tc.outcomes) || m.applies != m.rollbacks {
		t.Fatalf("status=%s evidence=%+v calls=%d apply=%d rollback=%d", m.Status(), e, len(commands), m.applies, m.rollbacks)
	}
	if e.ReusableDetection() != (tc.confidence == "confirmed") {
		t.Fatal("unsafe reuse decision")
	}
	if tc.confidence != "" && len(tc.outcomes) > 1 {
		if len(e.Attempts) != len(tc.outcomes) {
			t.Fatal("missing attempts")
		}
		for i, a := range e.Attempts {
			if a.DurationSeconds < 0 || a.TimeoutSeconds != 1 || a.Role == "" || !reflect.DeepEqual(a.Args, e.Args) {
				t.Fatalf("attempt %d: %+v", i, a)
			}
		}
	}
	for i, c := range commands {
		if !reflect.DeepEqual(selections[0], selections[i]) || !reflect.DeepEqual(commands[0].Env[len(commands[0].Env)-1:], c.Env[len(c.Env)-1:]) {
			t.Fatal("selection/temporary scope differs")
		}
		if i > 0 && commands[0].Dir == c.Dir {
			t.Fatal("contaminated worker reused")
		}
	}
	// Three process attempts have at most three seconds of process budget.
	if time.Since(started) > 5*time.Second {
		t.Fatal("confirmation exceeded bound")
	}
	t.Logf("%d attempts, elapsed %s", len(commands), time.Since(started))
}

func TestConfirmationProcess(t *testing.T) {
	if os.Getenv("GO_TEST_PROCESS") != "1" {
		return
	}
	index := os.Getenv("CONFIRM_INDEX")
	want := "mutant"
	if index == "1" {
		want = "pristine"
	}
	state, err := os.ReadFile("state")
	if err != nil || string(state) != want {
		fmt.Fprintln(os.Stderr, "not pristine/matching:", string(state), err)
		os.Exit(2)
	}
	if index != "0" {
		for _, name := range []string{".git", "contaminated"} {
			if _, err := os.Lstat(name); !os.IsNotExist(err) {
				os.Exit(2)
			}
		}
	}
	_, contaminationErr := os.Stat("contaminated")
	// Every attempt dirties its copy. Neither a baseline nor a repeat may
	// inherit test-created files from another attempt.
	if err := os.WriteFile("contaminated", []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	switch os.Getenv("CONFIRM_RESULT") {
	case "contamination":
		if contaminationErr == nil {
			fmt.Fprintln(os.Stdout, `{"Action":"fail","Package":"example.test","Test":"TestCausality"}`)
			os.Exit(1)
		}
	case "timeout":
		time.Sleep(10 * time.Second)
	case "infra":
		os.Exit(2)
	case "fail":
		fmt.Fprintln(os.Stdout, `{"Action":"fail","Package":"example.test","Test":"TestCausality"}`)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestExecutorConfirmationRealMutation(t *testing.T) {
	for _, integration := range []bool{false, true} {
		t.Run(fmt.Sprintf("integration=%t", integration), func(t *testing.T) {
			viperSet(map[string]any{configuration.RunConfirmOutcomesKey: true, configuration.RunWorkersKey: 1, configuration.RunIntegrationMode: integration, configuration.RunTagsKey: "confirmation", configuration.RunTestCPUKey: 1})
			defer viperReset()
			src := t.TempDir()
			source := "package example\nfunc Value() int { return 1+2 }\n"
			for name, content := range map[string]string{
				"go.mod":         "module example.com\n\ngo 1.21\n",
				"source.go":      source,
				"source_test.go": "//go:build confirmation\n\npackage example\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value()!=3 { t.Fatal(\"wrong value\") } }\n",
			} {
				if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m, expression := arithmeticMutant(t, src)
			m.SetStatus(mutator.Runnable)
			dealer := workdir.NewCachedDealer(t.TempDir(), src)
			defer dealer.Clean()
			executor := engine.NewExecutor(gomodule.GoModule{Name: "example.com", Root: src, CallingDir: "."}, dealer, 2*time.Second)
			started := time.Now()
			results, err := executor.Execute(context.Background(), []mutator.Mutator{m}, nil)
			if err != nil || len(results) != 1 {
				t.Fatalf("results=%v err=%v", results, err)
			}
			e := m.ExecutionEvidence()
			if m.Status() != mutator.Killed || !e.ReusableDetection() || len(e.Attempts) != 3 || len(e.Attempts[2].FailingTests) != 1 {
				t.Fatalf("status=%s evidence=%+v", m.Status(), e)
			}
			if expression.Op.String() != "+" {
				t.Fatal("AST not restored")
			}
			// #nosec G304 -- source path is a test-owned worker directory.
			restored, err := os.ReadFile(filepath.Join(m.Workdir(), "source.go"))
			if err != nil || string(restored) != source {
				t.Fatalf("worker not restored: %s %v", restored, err)
			}
			t.Logf("real Go confirmation: elapsed=%s initial=%.3fs baseline=%.3fs repeat=%.3fs", time.Since(started), e.Attempts[0].DurationSeconds, e.Attempts[1].DurationSeconds, e.Attempts[2].DurationSeconds)
		})
	}
}

func TestExecutorConfirmationEmptyCampaignDoesNotSnapshot(t *testing.T) {
	viperSet(map[string]any{configuration.RunConfirmOutcomesKey: true, configuration.RunWorkersKey: 1})
	defer viperReset()
	tmp := t.TempDir()
	// An empty campaign needs neither source access nor a snapshot. In
	// particular, it must succeed even if its source is no longer available.
	source := filepath.Join(t.TempDir(), "missing-source")
	dealer := workdir.NewCachedDealer(tmp, source)
	defer dealer.Clean()
	executor := engine.NewExecutor(gomodule.GoModule{Root: source, CallingDir: "."}, dealer, time.Second,
		engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			t.Error("empty campaign launched a process")

			return fakeExecCommandSuccess(ctx, name, args...)
		}))
	results, err := executor.Execute(context.Background(), nil, func(mutator.Mutator) {
		t.Error("empty campaign reported a completion")
	})
	if err != nil || len(results) != 0 {
		t.Fatalf("empty campaign: results=%v err=%v", results, err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty campaign created temporary files: %v %v", entries, err)
	}
}
