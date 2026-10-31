package cmd_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/ledger"
)

func TestCLIIncrementalRetainsBaselineAndIndependentExecution(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	cache := isolateOutcomeCache(t)
	t.Setenv("IMP_TEST_SECRET", "secret-not-for-persistence")
	marker := filepath.Join(t.TempDir(), "observations")
	t.Setenv("IMP_OBSERVATIONS", marker)
	dir := newModuleFixture(t)
	t.Chdir(dir)
	tests := `package fixture_test
import ("testing";"os";"example.com/fixture")
func TestValue(t *testing.T){
 f,err:=os.OpenFile(os.Getenv("IMP_OBSERVATIONS"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)}
 if _,err=f.WriteString("observation\n");err!=nil{t.Fatal(err)};if err=f.Close();err!=nil{t.Fatal(err)}
 if fixture.Value(1,2)!=3{t.Fatal("wrong sum")}
}
`
	writeFixture(t, filepath.Join(dir, "code_test.go"), tests)
	run := func(args ...string) []ledger.Evidence {
		t.Helper()
		configuration.Reset()
		out, err := executeCLI(context.Background(), t, "test", append([]string{"run", "--json", "--incremental"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Evidence []ledger.Evidence `json:"evidence"`
			Note     string            `json:"ledger_note"`
		}
		if err = json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if result.Note != "" {
			t.Fatal(result.Note)
		}

		return result.Evidence
	}
	observations := func() int {
		t.Helper()
		// #nosec G304 -- external observation file is owned by this test.
		data, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}

		return strings.Count(string(data), "observation\n")
	}
	for i := 0; i < 2; i++ {
		evidence := run()
		if len(evidence) != 1 || evidence[0].Provenance != "fresh" || evidence[0].Confirmations != i+1 {
			t.Fatal(evidence)
		}
		if count := observations(); count != 2*(i+1) {
			t.Fatalf("baseline plus fresh mutant: got %d observations", count)
		}
	}
	if evidence := run(); len(evidence) != 1 || evidence[0].Provenance != "reused" || evidence[0].Confidence != "confirmed" {
		t.Fatal(evidence)
	}
	if count := observations(); count != 5 {
		t.Fatalf("reused run must still execute coverage baseline: got %d observations", count)
	}
	// Test-only edits invalidate a historical kill; the production source and ID remain unchanged.
	writeFixture(t, filepath.Join(dir, "code_test.go"), strings.ReplaceAll(tests, "fixture.Value(1,2)!=3", "fixture.Value(1,2)==999"))
	if evidence := run(); len(evidence) != 1 || evidence[0].Provenance != "fresh" || evidence[0].Transition != "killed-to-lived" || evidence[0].Confirmations != 1 {
		t.Fatal(evidence)
	}
	if count := observations(); count != 7 {
		t.Fatalf("test edit did not execute baseline and mutant: %d", count)
	}
	if evidence := run("--incremental-refresh"); evidence[0].Confirmations != 1 || evidence[0].Previous != "" {
		t.Fatal(evidence)
	}
	files, err := filepath.Glob(filepath.Join(cache, "imp", "outcomes-v1", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	// #nosec G703 -- cache directory is a test-owned temporary directory.
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-not-for-persistence") {
		t.Fatal("raw environment secret persisted")
	}
	configuration.Reset()
	out, err := executeCLI(context.Background(), t, "test", "run", "--json", "--incremental=false")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\"evidence\"") {
		t.Fatal("disabled ledger emitted evidence")
	}
}

func TestCLIIncrementalOptionValidation(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	for _, args := range [][]string{{"--incremental-refresh"}, {"--incremental", "--incremental-ttl-hours=0"}, {"--incremental", "--incremental-ttl-hours=721"}, {"--incremental", "--incremental-max-entries=10001"}} {
		configuration.Reset()
		if _, err := executeCLI(context.Background(), t, "test", append([]string{"run"}, args...)...); err == nil {
			t.Fatal("invalid options accepted", args)
		}
	}
}

func isolateOutcomeCache(t testing.TB) string {
	t.Helper()
	// Isolating imp's user cache must not force unrelated Go compilation caches
	// into that temporary directory (especially in repeated workflow benchmarks).
	cacheOutput, err := exec.CommandContext(context.Background(), "go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", strings.TrimSpace(string(cacheOutput)))
	dir := t.TempDir()
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", dir)
	case "windows":
		t.Setenv("LocalAppData", dir)
	default:
		t.Setenv("XDG_CACHE_HOME", dir)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}

	return cache
}

func TestCLIIncrementalFailedReplayRefreshPreservesLedger(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	cache := isolateOutcomeCache(t)
	dir := newModuleFixture(t)
	t.Chdir(dir)
	out, err := executeCLI(context.Background(), t, "test", "run", "--json", "--incremental")
	if err != nil {
		t.Fatal(err)
	}
	var initial struct {
		Evidence []ledger.Evidence `json:"evidence"`
		Note     string            `json:"ledger_note"`
	}
	if err = json.Unmarshal([]byte(out), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Note != "" || len(initial.Evidence) != 1 {
		t.Fatalf("unexpected seed: %s", out)
	}
	files, err := filepath.Glob(filepath.Join(cache, "imp", "outcomes-v1", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	readLedger := func() []byte {
		t.Helper()
		// #nosec G703 -- the ledger path belongs to this test's isolated user cache.
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}

		return data
	}
	before := readLedger()
	for _, tc := range []struct{ name, id, resolution string }{
		{"missing", "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), "missing"},
		{"changed", initial.Evidence[0].ID, "changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "changed" {
				writeFixture(t, filepath.Join(dir, "code.go"), "package fixture\nfunc Value(a,b int)int{return b+a}\n")
			}
			configuration.Reset()
			out, err := executeCLI(context.Background(), t, "test", "run", "--json", "--incremental", "--incremental-refresh", "--mutant-ids", tc.id)
			if err == nil || !strings.Contains(err.Error(), "exact replay unresolved") {
				t.Fatalf("expected failed replay, got %v: %s", err, out)
			}
			var result replayDocument
			if err = json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.Total != 0 || len(result.Replay) != 1 || result.Replay[0].Resolution != tc.resolution {
				t.Fatalf("unexpected replay diagnostics: %s", out)
			}
			if string(before) != string(readLedger()) {
				t.Fatal("failed replay refresh replaced historical ledger")
			}
		})
	}
}
