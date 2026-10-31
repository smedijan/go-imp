package cmd_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/verification"
)

func verificationGit(t *testing.T, root string, args ...string) {
	t.Helper()
	// #nosec G204 -- test-owned fixture operations, with no inherited Git state.
	command := exec.Command("git", args...)
	command.Dir = root
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0="+filepath.Join(root, "no-hooks"), "GIT_CONFIG_KEY_1=commit.gpgSign", "GIT_CONFIG_VALUE_1=false")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture Git: %v: %s", err, out)
	}
}

func TestVerifyCLIReplayReportAndAtomicity(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	root := newModuleFixture(t)
	before := "package fixture_test\nimport (\"testing\";\"example.com/fixture\")\nfunc TestValue(t *testing.T){_ = fixture.Value(1,2)}\n"
	writeFixture(t, filepath.Join(root, "code_test.go"), before)
	verificationGit(t, root, "init")
	verificationGit(t, root, "config", "user.name", "Fixture")
	verificationGit(t, root, "config", "user.email", "fixture@example.invalid")
	verificationGit(t, root, "add", ".")
	verificationGit(t, root, "commit", "-m", "baseline")
	t.Chdir(root)
	payload, err := executeCLI(context.Background(), t, "test", "run", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	prior := filepath.Join(t.TempDir(), "prior.json")
	writeFixture(t, prior, payload)
	after := strings.Replace(before, "_ = fixture.Value(1,2)", "if fixture.Value(1,2)!=3 {t.Fatal(\"sum\")}", 1)
	writeFixture(t, filepath.Join(root, "code_test.go"), after)
	destination := filepath.Join(t.TempDir(), "verification.json")
	args := []string{"verify", "--baseline", "HEAD", "--package", ".", "--test", "TestValue", "--replay-report", prior, "--output", destination}
	if _, err := executeCLI(context.Background(), t, "test", args...); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- test-owned report destination.
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var document verification.Report
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Results) != 1 || document.Results[0].Outcome != "detected" || !document.Results[0].HistoricalImprovement {
		t.Fatalf("document: %+v", document)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executeCLI(ctx, t, "test", args...); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	// #nosec G304 -- test-owned report destination.
	retained, err := os.ReadFile(destination)
	if err != nil || string(retained) != string(contents) {
		t.Fatalf("previous report replaced after cancellation: %v", err)
	}
	// Unsupported source patches publish a complete inconclusive result, exit 1.
	writeFixture(t, filepath.Join(root, "code.go"), "package fixture\nfunc Value(a,b int)int{return a-b}\n")
	_, err = executeCLI(context.Background(), t, "test", args...)
	var exit interface{ ExitCode() int }
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("inconclusive exit: %v", err)
	}
	// #nosec G304 -- test-owned report destination.
	contents, err = os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	if document.Results[0].Outcome != "inconclusive" {
		t.Fatalf("unsupported patch: %+v", document)
	}
}
