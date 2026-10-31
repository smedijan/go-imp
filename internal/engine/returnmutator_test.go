//nolint:gosec // Fixture paths are created under t.TempDir.
package engine //nolint:testpackage // Exercise exact splices and backup lifecycle directly.

import (
	"bytes"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smedijan/go-imp/internal/mutator"
)

func returnMutatorFixture(t *testing.T, source string) *ReturnMutator {
	t.Helper()
	start := strings.Index(source, "flag()")
	position := token.Position{Filename: "source.go", Offset: start, Line: 2, Column: 20}

	return newReturnMutator("example", position, token.Pos(start+1), start+len("flag()"), []byte(source), "false", mutator.BooleanReturn)
}

func TestReturnMutatorExactSpliceAndRollback(t *testing.T) {
	t.Parallel()
	source := "package example\nfunc f() (int,bool,int) { return first(), /* before */ flag(), /* untouched */ last() } // end\n"
	m := returnMutatorFixture(t, source)
	for range 2 {
		m.SetWorkdir(t.TempDir())
		filename := filepath.Join(m.Workdir(), "source.go")
		if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := m.Apply(); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(source, "flag()", "false", 1)
		if string(got) != want {
			t.Fatalf("splice changed untouched bytes: %s", got)
		}
		if !bytes.Equal(m.MutatedSnippet(), []byte(want)) {
			t.Fatal("preview differs from apply")
		}
		if err := m.Apply(); err == nil {
			t.Fatal("double apply should fail")
		}
		if err := m.Rollback(); err != nil {
			t.Fatal(err)
		}
		got, err = os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != source {
			t.Fatal("rollback did not restore exact bytes")
		}
		if err := m.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	command := []string{"go", "test", "./..."}
	m.SetTestCommand(command)
	command[0] = "changed"
	if m.TestCommand()[0] != "go" {
		t.Fatal("command was not copied")
	}
}

func TestReturnMutatorFailedApplyAndRollback(t *testing.T) {
	t.Parallel()
	source := "package example\nfunc f()bool{return flag()}\n"
	m := returnMutatorFixture(t, source)
	m.SetWorkdir(t.TempDir())
	if err := m.Apply(); err == nil {
		t.Fatal("missing file should fail")
	}
	if err := m.Rollback(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(m.Workdir(), "source.go")
	stale := strings.Replace(source, "flag()", "other()", 1)
	if err := os.WriteFile(filename, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); err == nil {
		t.Fatal("stale expression should fail")
	}
	if err := m.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stale {
		t.Fatal("failed apply overwrote source")
	}
	if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filename, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(); err == nil {
		t.Fatal("rollback to directory should fail")
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != source {
		t.Fatal("failed rollback discarded backup")
	}
}

func TestReturnMutatorConcurrentWorkerIsolation(t *testing.T) {
	t.Parallel()
	source := "package example\nfunc f()bool{return flag()}\n"
	var wg sync.WaitGroup
	for range 8 {
		m := returnMutatorFixture(t, source)
		m.SetWorkdir(t.TempDir())
		filename := filepath.Join(m.Workdir(), "source.go")
		if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			if err := m.Apply(); err != nil {
				t.Error(err)

				return
			}
			if err := m.Rollback(); err != nil {
				t.Error(err)

				return
			}
			got, err := os.ReadFile(filename)
			if err != nil || string(got) != source {
				t.Errorf("worker source: %s %v", got, err)
			}
		})
	}
	wg.Wait()
}
