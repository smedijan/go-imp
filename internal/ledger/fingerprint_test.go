package ledger_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/ledger"
)

func write(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- fixture paths are constructed in the test-owned temporary tree.
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t testing.TB) (string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "module")
	dep := filepath.Join(base, "dep")
	write(t, filepath.Join(root, "go.mod"), "module example.com/main\n\ngo 1.21\n\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n")
	write(t, filepath.Join(root, "main.go"), "package main\nimport (\"example.com/dep\"; _ \"embed\")\n//go:embed data.txt\nvar data string\nfunc Value() int { return dep.Value() }\n")
	write(t, filepath.Join(root, "main_test.go"), "package main\nimport \"testing\"\nfunc TestValue(t *testing.T){if Value()!=1 {t.Fatal(Value())}}\n")
	write(t, filepath.Join(root, "data.txt"), "embedded")
	write(t, filepath.Join(root, ".hidden", "fixture.txt"), "hidden fixture")
	write(t, filepath.Join(root, "testdata", "fixture.txt"), "fixture")
	write(t, filepath.Join(dep, "go.mod"), "module example.com/dep\n\ngo 1.21\n")
	write(t, filepath.Join(dep, "dep.go"), "package dep\nfunc Value() int {return 1}\n")

	return root, dep
}
func TestFingerprintInvalidation(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("CGO_ENABLED", "0")
	root, dep := fixture(t)
	path := filepath.Join(t.TempDir(), "ledger.json")
	fingerprint := func(settings, tags string) string {
		t.Helper()
		value, err := ledger.Fingerprint(context.Background(), root, path, settings, tags)
		if err != nil {
			t.Fatal(err)
		}

		return value
	}
	baseline := fingerprint("settings", "")
	if again := fingerprint("settings", ""); again != baseline {
		t.Fatal("unstable fingerprint")
	}
	for _, name := range []string{filepath.Join(root, "main_test.go"), filepath.Join(root, "testdata", "fixture.txt"), filepath.Join(root, "data.txt"), filepath.Join(root, ".hidden", "fixture.txt"), filepath.Join(dep, "dep.go"), filepath.Join(dep, "go.mod")} {
		t.Run(filepath.Base(name), func(t *testing.T) {
			info, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			// #nosec G304 -- named fixture belongs to this test.
			before, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			write(t, name, string(before)+"\n")
			if fingerprint("settings", "") == baseline {
				t.Fatal("input change not invalidated", name)
			}
			write(t, name, string(before))
			if err = os.Chtimes(name, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if fingerprint("settings", "") != baseline {
				t.Fatal("restored contents and timestamp did not restore fingerprint")
			}
		})
	}

	for _, name := range []string{filepath.Join(root, "testdata", "fixture.txt"), filepath.Join(root, "testdata")} {
		t.Run("timestamp-only-"+filepath.Base(name), func(t *testing.T) {
			info, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			changed := info.ModTime().Add(-2 * time.Hour)
			if err = os.Chtimes(name, changed, changed); err != nil {
				t.Fatal(err)
			}
			if fingerprint("settings", "") == baseline {
				t.Fatal("timestamp-only input change not invalidated", name)
			}
			if err = os.Chtimes(name, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if fingerprint("settings", "") != baseline {
				t.Fatal("restoring timestamp did not restore fingerprint")
			}
		})
	}
	if fingerprint("changed test flags", "") == baseline {
		t.Fatal("flags not invalidated")
	}
	if fingerprint("settings", "customtag") == baseline {
		t.Fatal("build tags not invalidated")
	}
	previousSecret, hadSecret := os.LookupEnv("IMP_TEST_SECRET")
	t.Setenv("IMP_TEST_SECRET", "very-secret-value")
	if fingerprint("settings", "") == baseline {
		t.Fatal("environment not invalidated")
	}
	if hadSecret {
		t.Setenv("IMP_TEST_SECRET", previousSecret)
	} else {
		if err := os.Unsetenv("IMP_TEST_SECRET"); err != nil {
			t.Fatal(err)
		}
	}
	previousToolchain := os.Getenv("GOTOOLCHAIN")
	if previousToolchain == "local" {
		t.Setenv("GOTOOLCHAIN", "auto")
	} else {
		t.Setenv("GOTOOLCHAIN", "local")
	}
	if fingerprint("settings", "") == baseline {
		t.Fatal("toolchain environment not invalidated")
	}
}
func TestFingerprintWorkspaceAndUnsupportedInputs(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	root, _ := fixture(t)
	path := filepath.Join(t.TempDir(), "ledger.json")
	work := filepath.Join(filepath.Dir(root), "go.work")
	write(t, work, "go 1.21\nuse (\n./module\n./dep\n)\n")
	t.Setenv("GOWORK", work)
	before, err := ledger.Fingerprint(context.Background(), root, path, "settings", "")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(filepath.Dir(root), "workspace-fixture.bin"), "data")
	after, err := ledger.Fingerprint(context.Background(), root, path, "settings", "")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("workspace fixtures not invalidated")
	}
	if err = os.Symlink(filepath.Join(root, "data.txt"), filepath.Join(root, "linked-input")); err != nil {
		if runtime.GOOS == "windows" {
			t.Log("symlink creation unavailable; workspace hashing verified")

			return
		}
		t.Fatal(err)
	}
	if _, err = ledger.Fingerprint(context.Background(), root, path, "settings", ""); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestFingerprintCancellationAndOverlap(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	root, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ledger.Fingerprint(ctx, root, filepath.Join(t.TempDir(), "ledger"), "", ""); err == nil {
		t.Fatal("cancelled fingerprint accepted")
	}
	if _, err := ledger.Fingerprint(context.Background(), root, filepath.Join(root, "ledger"), "", ""); err == nil {
		t.Fatal("overlapping storage accepted")
	}
}

func TestFingerprintToolchainAndUnsupportedMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable real-Go tests above cover Windows; this metadata adapter uses /bin/sh")
	}
	root := t.TempDir()
	toolchain := t.TempDir()
	bin := t.TempDir()
	write(t, filepath.Join(root, "fixture.txt"), "fixture")
	write(t, filepath.Join(root, ".git", "fixture.txt"), "hidden fixture")
	write(t, filepath.Join(toolchain, "compiler"), "compiler-v1")
	env, err := json.Marshal(map[string]string{"GOROOT": toolchain, "GOFLAGS": "-p=2"})
	if err != nil {
		t.Fatal(err)
	}
	modules, err := json.Marshal(map[string]string{"Dir": root})
	if err != nil {
		t.Fatal(err)
	}
	metadata := func(packages, flags string) {
		t.Helper()
		payload := strings.ReplaceAll(string(env), "-p=2", flags)
		script := "#!/bin/sh\ncase \"$1 $2\" in\n'env -json') printf '%s' '" + payload + "';;\n'version ') printf '%s' 'go version fixture';;\n'list -deps') printf '%s' '" + packages + "';;\n'list -m') printf '%s' '" + string(modules) + "';;\n*) exit 1;;\nesac\n"
		write(t, filepath.Join(bin, "go"), script)
		// #nosec G302 -- the test metadata adapter must be executable.
		if err := os.Chmod(filepath.Join(bin, "go"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	metadata("{}", "-p=2")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(t.TempDir(), "ledger")
	fingerprint := func() (string, error) { return ledger.Fingerprint(context.Background(), root, path, "", "") }
	before, err := fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".git", "fixture.txt"), "changed hidden fixture")
	hidden, err := fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if before == hidden {
		t.Fatal("root .git fixture not invalidated")
	}
	before = hidden
	write(t, filepath.Join(toolchain, "compiler"), "compiler-v2")
	after, err := fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("changed toolchain bytes not invalidated")
	}
	for _, packages := range []string{`{"CgoFiles":["cgo.go"]}`, `{"SFiles":["custom.s"]}`, `{"Incomplete":true}`, `{"Error":{}}`, "{"} {
		metadata(packages, "-p=2")
		if _, err = fingerprint(); err == nil {
			t.Fatal("unsafe metadata accepted", packages)
		}
	}
	metadata("{}", "-overlay=external.json")
	if _, err = fingerprint(); err == nil {
		t.Fatal("external GOFLAGS inputs accepted")
	}
}
