package cmd_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/cmd"
)

// executeCLI exercises the same entry point as cmd/imp. Tests using it must not
// run in parallel because the CLI owns process arguments and standard output.
func executeCLI(ctx context.Context, t *testing.T, version string, args ...string) (string, error) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	previousArgs, previousOut := os.Args, os.Stdout
	os.Args, os.Stdout = append([]string{"imp"}, args...), out
	defer func() { os.Args, os.Stdout = previousArgs, previousOut }()
	err = cmd.Execute(ctx, version)
	if _, seekErr := out.Seek(0, io.SeekStart); seekErr != nil {
		t.Fatal(seekErr)
	}
	payload, readErr := io.ReadAll(out)
	if readErr != nil {
		t.Fatal(readErr)
	}

	return string(payload), err
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newModuleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/fixture\n\ngo 1.25.0\n")
	writeFixture(t, filepath.Join(dir, ".imp.yaml"), "{}\n")
	writeFixture(t, filepath.Join(dir, "code.go"), "package fixture\nfunc Value(a,b int)int{return a+b}\n")
	writeFixture(t, filepath.Join(dir, "code_test.go"), "package fixture_test\nimport (\"testing\"; \"example.com/fixture\")\nfunc TestValue(t *testing.T){if fixture.Value(1,2)!=3{t.Fatal(\"wrong sum\")}}\n")

	return dir
}
