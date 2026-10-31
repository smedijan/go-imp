package cmd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
)

func incrementalConfirmationFixture(t *testing.T, inconsistent bool) func() int {
	t.Helper()
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	isolateOutcomeCache(t)
	marker := filepath.Join(t.TempDir(), "observations")
	t.Setenv("IMP_OBSERVATIONS", marker)
	dir := newModuleFixture(t)
	t.Chdir(dir)
	// The deliberately inconsistent variant controls repeated mutant outcomes
	// externally to exercise refusal of unreliable evidence, not cache validity.
	tests := fmt.Sprintf(`package fixture_test
import ("testing";"os";"strings";"example.com/fixture")
func TestValue(t *testing.T){
 path:=os.Getenv("IMP_OBSERVATIONS")
 f,err:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)}
 if _,err=f.WriteString("observation\n");err!=nil{t.Fatal(err)};if err=f.Close();err!=nil{t.Fatal(err)}
 data,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
 if fixture.Value(1,2)!=3{
  if %t && strings.Count(string(data),"observation\n")%%4==0{return}
  t.Fatal("wrong sum")
 }
}
`, inconsistent)
	writeFixture(t, filepath.Join(dir, "code_test.go"), tests)

	return func() int {
		t.Helper()
		// #nosec G304 -- this observation file belongs to the test's temporary directory.
		data, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}

		return strings.Count(string(data), "observation\n")
	}
}

func runIncrementalConfirmation(t *testing.T, strict bool, id string) incrementalReport {
	t.Helper()
	configuration.Reset()
	args := []string{"run", "--json", "--incremental", "--workers=1", fmt.Sprintf("--confirm-outcomes=%t", strict)}
	if id != "" {
		args = append(args, "--mutant-ids", id)
	}
	out, err := executeCLI(context.Background(), t, "test", args...)
	if err != nil {
		t.Fatal(err)
	}
	var result incrementalReport
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Note != "" {
		t.Fatal(result.Note)
	}
	assertIncrementalReportEvidence(t, result)
	if id != "" {
		for _, files := range result.Groups {
			for _, findings := range files {
				for _, finding := range findings {
					if finding.ID != id || finding.Context.Selection.Replay != "matched" {
						t.Fatal("current exact-replay identity/selection lost", finding)
					}
				}
			}
		}
	}

	return result
}

func TestCLIIncrementalConfirmationModesReplay(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	observations := incrementalConfirmationFixture(t, false)
	id := ""
	expectedObservations := 0
	for _, tc := range []struct {
		strict               bool
		provenance           string
		count, confirmations int
	}{
		{false, "fresh", 2, 1}, {false, "fresh", 2, 2}, {false, "reused", 1, 2},
		{true, "fresh", 4, 1}, {true, "fresh", 4, 2}, {true, "reused", 1, 2},
		{false, "fresh", 2, 1},
	} {
		result := runIncrementalConfirmation(t, tc.strict, id)
		outcome := result.Evidence[0]
		if outcome.Provenance != tc.provenance || outcome.Confirmations != tc.confirmations {
			t.Fatalf("strict=%t: %+v", tc.strict, result)
		}
		if id == "" {
			id = outcome.ID
		}
		findings := result.Groups["KILLED"]["code.go"]
		if len(findings) != 1 {
			t.Fatalf("expected killed replay finding: %+v", result)
		}
		observed := findings[0].Execution
		if tc.provenance == "fresh" {
			if tc.strict {
				if !observed.ReusableDetection() || len(observed.Attempts) != 3 {
					t.Fatal("strict fresh detection lacks confirmation", observed)
				}
			} else if observed.Confidence != "" || len(observed.Attempts) != 0 {
				t.Fatal("off mode unexpectedly claimed strict confirmation", observed)
			}
		}
		expectedObservations += tc.count
		if count := observations(); count != expectedObservations {
			t.Fatalf("strict=%t: observations=%d want=%d", tc.strict, count, expectedObservations)
		}
	}
}

func TestCLIIncrementalUnreliableConfirmationIsNeverReused(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	observations := incrementalConfirmationFixture(t, true)
	id := ""
	for i := 0; i < 3; i++ {
		result := runIncrementalConfirmation(t, true, id)
		outcome := result.Evidence[0]
		if outcome.Provenance != "fresh" || outcome.Confidence == "confirmed" || outcome.Confirmations != 0 {
			t.Fatal("unreliable observation promoted", result)
		}
		id = outcome.ID
		findings := result.Groups["NOT VIABLE"]["code.go"]
		if len(findings) != 1 || findings[0].Execution.Confidence != "inconsistent" || findings[0].Execution.ReusableDetection() || len(findings[0].Execution.Attempts) != 3 {
			t.Fatal("missing actual inconsistent execution evidence", result)
		}
		if count := observations(); count != 4*(i+1) {
			t.Fatalf("unreliable mutant did not execute fresh confirmation: %d", count)
		}
	}
}
