//nolint:gosec // Fixture paths are created under t.TempDir.
package engine_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestReturnReplacementEvaluationAndInitialization(t *testing.T) {
	returnTestConfig(t)
	source := `package example
var events []string
func setup() int { events = append(events, "init"); return 7 }
func flag() bool { panic("replaced Boolean expression executed") }
func fail() error { panic("replaced error expression executed") }
func tail() int { events = append(events, "tail"); return 9 }
func booleanResult() (int, bool, int) { x := setup(); return x, /* kept */ flag(), tail() }
func errorResult() (int, error, int) { x := setup(); return x, /* kept */ fail(), tail() }
`
	mutations := discoverReturns(t, fstest.MapFS{"source.go": {Data: []byte(source)}}, engine.CodeData{})
	if len(mutations) != 3 {
		t.Fatalf("got %d mutations, want two Boolean and one error", len(mutations))
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/evaluation\n\ngo 1.23\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, m := range mutations {
		target, expected := "booleanResult", "true"
		if m.Type() == mutator.ErrorReturn {
			target, expected = "errorResult", "nil"
		} else if strings.Contains(string(m.MutatedSnippet()), "/* kept */ false, tail()") {
			expected = "false"
		}
		testSource := fmt.Sprintf(`package example
import ("testing";"reflect")
func TestEvaluation(t *testing.T) {
 events = nil
 first, replaced, last := %s()
 if first != 7 || last != 9 || replaced != %s { t.Fatalf("wrong results: %%v %%v %%v", first, replaced, last) }
 if !reflect.DeepEqual(events, []string{"init", "tail"}) { t.Fatalf("wrong evaluation order: %%v",events) }
}
`, target, expected)
		if err := os.WriteFile(filepath.Join(root, "source.go"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "source_test.go"), []byte(testSource), 0o600); err != nil {
			t.Fatal(err)
		}
		m.SetWorkdir(root)
		if err := m.Apply(); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("go", "test", "-race", "./...")
		command.Dir = root
		output, err := command.CombinedOutput()
		if rollbackErr := m.Rollback(); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if err != nil {
			t.Fatalf("candidate %d: %v\n%s", index, err, output)
		}
		restored, err := os.ReadFile(filepath.Join(root, "source.go"))
		if err != nil {
			t.Fatal(err)
		}
		if string(restored) != source {
			t.Fatal("evaluation test did not restore exact source")
		}
	}
}
