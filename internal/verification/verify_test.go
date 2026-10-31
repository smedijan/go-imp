package verification //nolint:testpackage // Inspect metadata-free snapshot contents and attribution classifiers directly.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

// Every test-owned Git subprocess strips ALL inherited Git state, including
// config-count and hook variables, rather than relying on caller cleanup.
func fixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	// #nosec G204 -- test-owned Git arguments are explicit fixture operations.
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0="+filepath.Join(root, "no-hooks"), "GIT_CONFIG_KEY_1=commit.gpgSign", "GIT_CONFIG_VALUE_1=false")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}

	return strings.TrimSpace(string(out))
}
func put(t *testing.T, root, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}
func repository(t *testing.T, source, test string) string {
	t.Helper()
	root := t.TempDir()
	fixtureGit(t, root, "init")
	fixtureGit(t, root, "config", "user.name", "Fixture")
	fixtureGit(t, root, "config", "user.email", "fixture@example.invalid")
	put(t, root, "go.mod", "module example.test/verify\n\ngo 1.21\n")
	put(t, root, "value.go", source)
	put(t, root, "value_test.go", test)
	fixtureGit(t, root, "add", ".")
	fixtureGit(t, root, "commit", "-m", "baseline")

	return root
}

const production = "package sample\nfunc Value(n int) bool { return n > 0 }\n"
const ignored = "package sample\nimport \"testing\"\nfunc TestValue(t *testing.T) { _ = Value(1) }\n"
const caught = "package sample\nimport \"testing\"\nfunc TestValue(t *testing.T) { if !Value(1) { t.Fatal(\"positive\") }; if Value(0) { t.Fatal(\"zero\") } }\n"

func targetID(t *testing.T, root string) string {
	t.Helper()
	mod, err := gomodule.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	discover := engine.New(mod, engine.CodeData{}, discoveryOnly{})
	found, err := discover.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range found.Mutants {
		if m.Type() == mutator.ConditionalsBoundary {
			identified, ok := m.(mutator.Identified)
			if !ok {
				t.Fatal("mutant missing identity")
			}

			return identified.Identity().ID
		}
	}
	t.Fatal("missing boundary mutant")

	return ""
}
func configure(t *testing.T) {
	t.Helper()
	if err := configuration.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, family := range mutator.Types {
		configuration.Set(configuration.MutantTypeEnabledKey(family), configuration.IsDefaultEnabled(family))
	}
	t.Cleanup(viper.Reset)
}
func runFixture(t *testing.T, root, id string) Result {
	t.Helper()
	document, err := Run(context.Background(), Options{Root: root, Baseline: "HEAD", Package: ".", Test: "TestValue", IDs: []string{id}, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Results) != 1 {
		t.Fatalf("results: %+v", document)
	}

	return document.Results[0]
}

func TestVerificationTestOnlyTransitions(t *testing.T) {
	configure(t)
	for _, tc := range []struct {
		name, before, after, want string
		history                   bool
	}{
		{"caught", ignored, caught, "detected", true},
		{"ignored", ignored, strings.Replace(ignored, "Value(1)", "Value(2)", 1), "ignored", false},
		{"baseline-failing", strings.Replace(ignored, "_ = Value(1)", "t.Fatal(\"existing failure\")", 1), caught, "baseline-failing", false},
		{"new-test", "package sample\nimport \"testing\"\nfunc TestOther(t *testing.T) {}\n", "package sample\nimport \"testing\"\nfunc TestOther(t *testing.T) {}\nfunc TestValue(t *testing.T) { if Value(0) {t.Fatal(\"zero\")} }\n", "detected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repository(t, production, tc.before)
			id := targetID(t, root)
			put(t, root, "value_test.go", tc.after)
			result := runFixture(t, root, id)
			if result.Outcome != tc.want || result.HistoricalImprovement != tc.history {
				t.Fatalf("got %+v", result)
			}
			if len(result.Observations) < 4 {
				t.Fatalf("missing repeated evidence: %+v", result)
			}
			before, current, _, err := snapshots(context.Background(), root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range []snapshot{before, current} {
				directory := t.TempDir()
				if err := s.write(directory); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(filepath.Join(directory, ".git")); !os.IsNotExist(err) {
					t.Fatalf("snapshot copied Git metadata: %v", err)
				}
			}
		})
	}
}

func TestVerificationStaleAndRemoved(t *testing.T) {
	configure(t)
	root := repository(t, production, ignored)
	id := targetID(t, root)
	put(t, root, "value_test.go", caught)
	parts := strings.Split(id, ":")
	parts[2] = strings.Repeat("0", 64)
	if result := runFixture(t, root, strings.Join(parts, ":")); result.Outcome != "stale" || len(result.Observations) != 0 {
		t.Fatalf("stale: %+v", result)
	}
	parts[1] = strings.Repeat("0", 64)
	if result := runFixture(t, root, strings.Join(parts, ":")); result.Outcome != "removed" {
		t.Fatalf("removed: %+v", result)
	}
}

func TestVerificationRejectsSourceAndAmbiguousTestChanges(t *testing.T) {
	configure(t)
	for _, name := range []string{"source", "module", "helper", "unrelated", "selector"} {
		t.Run(name, func(t *testing.T) {
			root := repository(t, production, ignored)
			id := targetID(t, root)
			put(t, root, "value_test.go", caught)
			switch name {
			case "source":
				put(t, root, "value.go", production+"// edit\n")
			case "module":
				put(t, root, "go.mod", "module other.test/value\n\ngo 1.21\n")
			case "helper":
				put(t, root, "value_test.go", caught+"func helper() {}\n")
			case "unrelated":
				put(t, root, "other_test.go", "package sample\nimport \"testing\"\nfunc TestOther(t *testing.T){t.Fatal(\"unrelated\")}\n")
			case "selector":
				if err := validateSelector("./...", "TestValue|TestOther"); err == nil {
					t.Fatal("accepted ambiguous selector")
				}

				return
			}
			result := runFixture(t, root, id)
			if result.Outcome != "inconclusive" || len(result.Observations) != 0 {
				t.Fatalf("got %+v", result)
			}
		})
	}
}

func TestVerificationGitIsolationAndGitDependentFailure(t *testing.T) {
	configure(t)
	victim := repository(t, production, ignored)
	// #nosec G304 -- sacrificial test-owned victim metadata.
	config, err := os.ReadFile(filepath.Join(victim, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- sacrificial test-owned victim metadata.
	head, err := os.ReadFile(filepath.Join(victim, ".git", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- sacrificial test-owned victim metadata.
	index, err := os.ReadFile(filepath.Join(victim, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(victim, ".git"))
	t.Setenv("GIT_WORK_TREE", victim)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(victim, ".git", "index"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", victim)
	// Constructing a second fixture under poisoned state must not touch victim.
	before := "package sample\nimport (\"testing\";\"os/exec\")\nfunc TestValue(t *testing.T) { _ = Value(1); if err:=exec.Command(\"git\",\"rev-parse\",\"--show-toplevel\").Run();err!=nil{t.Fatal(\"Git-dependent setup\")} }\n"
	root := repository(t, production, before)
	id := targetID(t, root)
	put(t, root, "value_test.go", strings.Replace(before, "_ = Value(1)", "if Value(0) {t.Fatal(\"zero\")}", 1))
	result := runFixture(t, root, id)
	if result.Outcome != "baseline-failing" || result.HistoricalImprovement {
		t.Fatalf("Git setup failure became kill: %+v", result)
	}
	for name, want := range map[string][]byte{"config": config, "HEAD": head, "index": index} {
		// #nosec G304 -- enumerated sacrificial victim metadata files.
		got, err := os.ReadFile(filepath.Join(victim, ".git", name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("victim %s changed: %v", name, err)
		}
	}
}

func TestVerificationClassification(t *testing.T) {
	options := Options{Package: ".", Test: "TestValue"}
	for _, tc := range []struct {
		name     string
		status   mutator.Status
		category string
		tests    []mutator.FailingTest
		want     string
	}{
		{"unrelated", mutator.Killed, "test", []mutator.FailingTest{{Package: "example.test/verify", Test: "TestOther"}}, "unrelated"},
		{"invalid", mutator.NotViable, "build-setup", nil, "invalid"},
		{"timeout", mutator.TimedOut, "timeout", nil, "timeout"},
		{"subtest", mutator.Killed, "test", []mutator.FailingTest{{Package: "example.test/verify", Test: "TestValue/zero"}, {Package: "example.test/verify", Test: "TestValue"}}, "kill"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(Observation{Status: tc.status, Evidence: &mutator.ExecutionEvidence{Category: tc.category, JSONValid: true, FailingTests: tc.tests}}, "example.test/verify", options)
			if got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestVerificationUnrelatedPackageFailure(t *testing.T) {
	configure(t)
	// TestMain fails only on the mutant, after the selected test has passed.
	// A package exit is not attributed to that passing changed test.
	before := `package sample
import ("testing"; "os")
func TestValue(t *testing.T) { _ = Value(1) }
func TestMain(m *testing.M) { code:=m.Run(); if Value(0) { os.Exit(1) }; os.Exit(code) }
`
	root := repository(t, production, before)
	id := targetID(t, root)
	put(t, root, "value_test.go", strings.Replace(before, "_ = Value(1)", "_ = Value(2)", 1))
	result := runFixture(t, root, id)
	if result.Outcome != "unrelated" || result.HistoricalImprovement {
		t.Fatalf("package failure attributed to changed test: %+v", result)
	}
}

func TestVerificationObservedInstability(t *testing.T) {
	configure(t)
	before := `package sample
import ("testing"; "os")
func TestValue(t *testing.T) { _ = Value(1); if os.Getenv("IMP_VERIFY_MARKER")=="unused" {t.Fatal("unused")} }
`
	root := repository(t, production, before)
	id := targetID(t, root)
	marker := filepath.Join(t.TempDir(), "alternating")
	t.Setenv("IMP_VERIFY_MARKER", marker)
	after := `package sample
import ("testing"; "os")
func TestValue(t *testing.T) {
 _ = Value(1)
 marker:=os.Getenv("IMP_VERIFY_MARKER")
 if _,err:=os.Stat(marker);err==nil { _=os.Remove(marker);t.Fatal("observed instability") }
 if err:=os.WriteFile(marker,[]byte("present"),0600);err!=nil{t.Fatal(err)}
}
`
	put(t, root, "value_test.go", after)
	result := runFixture(t, root, id)
	if result.Outcome != "flaky" || result.HistoricalImprovement {
		t.Fatalf("instability missed: %+v", result)
	}
}

func TestVerificationIgnoresDefaultRunDiffAndDryRun(t *testing.T) {
	configure(t)
	root := repository(t, production, ignored)
	id := targetID(t, root)
	put(t, root, "value_test.go", caught)
	configuration.Set(configuration.RunDiffRef, "HEAD")
	configuration.Set(configuration.RunDryRunKey, true)
	result := runFixture(t, root, id)
	if result.Outcome != "detected" || result.Application == "not-applied" {
		t.Fatalf("explicit target skipped by run config: %+v", result)
	}
}

func TestVerificationSnapshotOmitsGitfilePointer(t *testing.T) {
	configure(t)
	root := repository(t, production, ignored)
	metadata := filepath.Join(t.TempDir(), "metadata")
	if err := os.Rename(filepath.Join(root, ".git"), metadata); err != nil {
		t.Fatal(err)
	}
	pointer := "gitdir: " + metadata + "\n"
	put(t, root, ".git", pointer)
	// A gitfile has the same dangerous copy shape as a linked-worktree pointer,
	// without creating a worktree or borrowing any shared repository metadata.
	id := targetID(t, root)
	put(t, root, "value_test.go", caught)
	before, current, _, err := snapshots(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []snapshot{before, current} {
		directory := t.TempDir()
		if err := s.write(directory); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(directory, ".git")); !os.IsNotExist(err) {
			t.Fatalf("copied live gitfile pointer: %v", err)
		}
	}
	result := runFixture(t, root, id)
	if result.Outcome != "detected" {
		t.Fatalf("gitfile fixture: %+v", result)
	}
	// #nosec G304 -- test-owned gitfile, never a live worktree's metadata.
	retained, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil || string(retained) != pointer {
		t.Fatalf("gitfile modified: %v", err)
	}
}

func TestVerificationRejectsIgnoredSnapshotInputs(t *testing.T) {
	configure(t)
	for _, name := range []string{"ignored.go", "assets/extra.txt", "runtime-input"} {
		t.Run(name, func(t *testing.T) {
			source := production
			if name == "assets/extra.txt" {
				source = "package sample\nimport \"embed\"\n//go:embed assets/*\nvar assets embed.FS\nfunc Value(n int) bool { return n > 0 }\n"
			}
			root := repository(t, source, ignored)
			if name == "assets/extra.txt" {
				if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
					t.Fatal(err)
				}
				put(t, root, "assets/original.txt", "baseline embedded asset")
				fixtureGit(t, root, "add", "assets/original.txt")
			}
			put(t, root, ".gitignore", name+"\n")
			fixtureGit(t, root, "add", ".gitignore")
			fixtureGit(t, root, "commit", "-m", "ignore input")
			id := targetID(t, root)
			put(t, root, "value_test.go", caught)
			contents := "input"
			if name == "ignored.go" {
				contents = "package sample\nfunc init() {}\n"
			}
			put(t, root, name, contents)
			result := runFixture(t, root, id)
			if result.Outcome != "inconclusive" || !strings.Contains(result.Reason, "ignored files") || len(result.Observations) != 0 {
				t.Fatalf("ignored input silently omitted: %+v", result)
			}
			// #nosec G304 -- confirm the test-owned ignored input was not deleted.
			retained, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(retained) != contents {
				t.Fatalf("ignored input changed: %v", err)
			}
		})
	}
}

func TestVerificationSnapshotOperationalFailure(t *testing.T) {
	configure(t)
	root := repository(t, production, ignored)
	id := targetID(t, root)
	_, err := Run(context.Background(), Options{Root: root, Baseline: "refs/heads/not-present", Package: ".", Test: "TestValue", IDs: []string{id}, Timeout: time.Second})
	if err == nil {
		t.Fatal("missing Git baseline must be an operational error, not a committed inconclusive report")
	}
}

func TestVerificationManyPassingSubtests(t *testing.T) {
	configure(t)
	before := `package sample
import "testing"
func TestValue(t *testing.T) { for i:=0;i<105;i++ { t.Run("passing",func(t *testing.T){_ = Value(1)}) } }
`
	root := repository(t, production, before)
	id := targetID(t, root)
	put(t, root, "value_test.go", strings.Replace(before, "_ = Value(1)", "if Value(0) {t.Fatal(\"zero\")}", 1))
	result := runFixture(t, root, id)
	if result.Outcome != "detected" || !result.HistoricalImprovement {
		t.Fatalf("passing subtests hid selected completion: %+v", result)
	}
	for _, observation := range result.Observations {
		if observation.Phase == "current-original" || observation.Phase == "baseline-original" || observation.Phase == "baseline-mutant" {
			evidence := observation.Evidence
			if !evidence.PassingTestsTruncated || len(evidence.PassingTests) != 100 || evidence.SelectedPassingTest == nil || evidence.SelectedPassingTest.Test != "TestValue" {
				t.Fatalf("selected completion/truncation missing: %+v", evidence)
			}
		}
	}
}

func TestVerificationRejectsExplicitExternalWorkspace(t *testing.T) {
	configure(t)
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.BooleanReturn), true)
	victim := repository(t, production, ignored)
	var metadata = map[string][]byte{}
	for _, name := range []string{"config", "HEAD", "index"} {
		// #nosec G304 -- sacrificial fixture metadata, not shared Git state.
		data, err := os.ReadFile(filepath.Join(victim, ".git", name))
		if err != nil {
			t.Fatal(err)
		}
		metadata[name] = data
	}
	root := repository(t, production, ignored)
	id := targetID(t, root)
	put(t, root, "value_test.go", caught)
	workspace := filepath.Join(t.TempDir(), "go.work")
	// The external workspace deliberately excludes the verification snapshot.
	put(t, filepath.Dir(workspace), "go.work", "go 1.27\nuse "+filepath.ToSlash(victim)+"\n")
	t.Setenv("GOWORK", workspace)
	t.Setenv("GIT_DIR", filepath.Join(victim, ".git"))
	t.Setenv("GIT_WORK_TREE", victim)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(victim, ".git", "index"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", victim)
	result := runFixture(t, root, id)
	if result.Outcome != "inconclusive" || result.HistoricalImprovement || len(result.Observations) != 0 || !strings.Contains(result.Reason, "active Go workspace") {
		t.Fatalf("active external workspace was silently disabled: %+v", result)
	}
	for name, want := range metadata {
		// #nosec G304 -- sacrificial fixture metadata, not shared Git state.
		data, err := os.ReadFile(filepath.Join(victim, ".git", name))
		if err != nil || string(data) != string(want) {
			t.Fatalf("victim %s changed: %v", name, err)
		}
	}
	// #nosec G304 -- test-owned external workspace.
	data, err := os.ReadFile(workspace)
	if err != nil || string(data) != "go 1.27\nuse "+filepath.ToSlash(victim)+"\n" {
		t.Fatalf("external workspace changed: %v", err)
	}
}

func TestVerificationRejectsImplicitParentWorkspace(t *testing.T) {
	configure(t)
	t.Setenv("GOWORK", "")
	original := repository(t, production, ignored)
	parent := t.TempDir()
	root := filepath.Join(parent, "module")
	if err := os.Rename(original, root); err != nil {
		t.Fatal(err)
	}
	id := targetID(t, root)
	put(t, root, "value_test.go", caught)
	workspace := "go 1.27\nuse ./module\n"
	put(t, parent, "go.work", workspace)
	result := runFixture(t, root, id)
	if result.Outcome != "inconclusive" || result.HistoricalImprovement || len(result.Observations) != 0 || !strings.Contains(result.Reason, "active Go workspace") {
		t.Fatalf("implicit parent workspace was silently disabled: %+v", result)
	}
	// #nosec G304 -- test-owned parent workspace, outside the repository root.
	retained, err := os.ReadFile(filepath.Join(parent, "go.work"))
	if err != nil || string(retained) != workspace {
		t.Fatalf("parent workspace modified: %v", err)
	}
	// An explicitly disabled workspace is not part of the original Go context.
	t.Setenv("GOWORK", "off")
	result = runFixture(t, root, id)
	if result.Outcome != "detected" || !result.HistoricalImprovement {
		t.Fatalf("inactive parent workspace rejected: %+v", result)
	}
}

func TestVerificationCanonicalRootAlias(t *testing.T) {
	configure(t)
	root := repository(t, production, ignored)
	id := targetID(t, root)
	put(t, root, "value_test.go", caught)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if _, _, _, err := snapshots(context.Background(), alias, "HEAD"); err != nil {
		t.Fatalf("same directory anchor rejected: %v", err)
	}
	result := runFixture(t, alias, id)
	if result.Outcome != "detected" || !result.HistoricalImprovement {
		t.Fatalf("root alias did not verify identical module: %+v", result)
	}
	// Root aliases do not grant permission to follow source-entry symlinks.
	if err := os.Symlink(filepath.Join(root, "value.go"), filepath.Join(root, "linked.go")); err != nil {
		t.Fatal(err)
	}
	result = runFixture(t, alias, id)
	if result.Outcome != "inconclusive" || len(result.Observations) != 0 || !strings.Contains(result.Reason, "symlinks") {
		t.Fatalf("source-entry symlink accepted through alias: %+v", result)
	}
}

func TestVerificationOriginalEnvironmentPreservesWorkspace(t *testing.T) {
	t.Setenv("GOWORK", "/external/go.work")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", "victim")
	t.Setenv("GIT_UNRECOGNIZED_HOOK_STATE", "must be stripped")
	workspace := 0
	for _, entry := range snapshotEnvironment() {
		if strings.HasPrefix(entry, "GIT_") {
			t.Fatalf("original workspace query inherited Git routing: %q", entry)
		}
		if strings.HasPrefix(entry, "GOWORK=") {
			workspace++
			if entry != "GOWORK=/external/go.work" {
				t.Fatalf("original workspace context modified: %q", entry)
			}
		}
	}
	if workspace != 1 {
		t.Fatalf("original workspace context missing or duplicated: %d", workspace)
	}
}

func TestVerificationRejectsDistinctRootAnchor(t *testing.T) {
	configure(t)
	root := repository(t, production, ignored)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := snapshots(context.Background(), child, "HEAD"); err == nil || !strings.Contains(err.Error(), "roots to coincide") {
		t.Fatalf("distinct Git/module directory anchor accepted: %v", err)
	}
}

func TestVerificationIgnoresGlobalConfirmation(t *testing.T) {
	configure(t)
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			configuration.Set(configuration.RunConfirmOutcomesKey, enabled)
			// Verification intentionally ignores run.diff and must never execute
			// the unrelated failing test, even with global confirmation enabled.
			configuration.Set(configuration.RunDiffRef, "not-a-valid-ref")
			unrelated := "func TestUnrelated(t *testing.T) { t.Fatal(\"not selected\") }\n"
			root := repository(t, production, ignored+unrelated)
			id := targetID(t, root)
			put(t, root, "value_test.go", caught+unrelated)
			result := runFixture(t, root, id)
			if result.Outcome != "detected" || !result.HistoricalImprovement || len(result.Observations) != 8 {
				t.Fatalf("verification changed with confirmation=%t: %+v", enabled, result)
			}
			for _, observation := range result.Observations {
				e := observation.Evidence
				if e == nil || len(e.Attempts) != 0 || e.Confidence != "" || !e.JSONValid {
					t.Fatalf("not direct selected-test evidence: %+v", e)
				}
				if !strings.Contains(strings.Join(e.Args, " "), "-run ^TestValue$ .") {
					t.Fatalf("selection broadened: %v", e.Args)
				}
				for _, failure := range e.FailingTests {
					if failure.Test != "TestValue" {
						t.Fatalf("unrelated attribution: %+v", failure)
					}
				}
			}
		})
	}
}
