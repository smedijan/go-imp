package diff

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bluekeyes/go-gitdiff/gitdiff"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/log"
)

// New creates a new Diff by parsing git diff output using the default command executor.
func New(mod gomodule.GoModule) (Diff, error) {
	return newWithCmd(exec.Command, mod)
}

type execCmd interface {
	CombinedOutput() ([]byte, error)
}

// NewWithCmd creates a new Diff by parsing git diff output using a custom command executor.
// This is useful for testing.
func NewWithCmd[T execCmd](cmdContext func(name string, args ...string) T) (Diff, error) {
	return newWithCmd(cmdContext, gomodule.GoModule{})
}

func newWithCmd[T execCmd](cmdContext func(name string, args ...string) T, mod gomodule.GoModule) (Diff, error) {
	diffRef := configuration.Get[string](configuration.RunDiffRef)
	if diffRef == "" {
		return nil, nil
	}

	log.Infoln("Gathering files diff...")

	cmd := cmdContext("git", "diff", "--merge-base", diffRef)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("an error occured while calling git diff: %w\n\n%s", err, out)
	}

	files, _, err := gitdiff.Parse(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("an error occured while parsing diff: %w", err)
	}

	result := newDiff(files)
	if mod.Root != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("could not determine working directory: %w", err)
		}
		rootOutput, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("could not determine Git repository root: %w\n\n%s", err, rootOutput)
		}
		result = normalizePaths(result, string(bytes.TrimSpace(rootOutput)), cwd, mod)
	}

	return result, nil
}

func normalizePaths(d Diff, repoRoot, cwd string, mod gomodule.GoModule) Diff {
	moduleRoot := mod.Root
	if !filepath.IsAbs(moduleRoot) {
		moduleRoot = filepath.Join(cwd, moduleRoot)
	}
	sourceRoot := filepath.Join(moduleRoot, mod.CallingDir)
	result := make(Diff, len(d))
	for name, changes := range d {
		absolute := filepath.Join(repoRoot, filepath.FromSlash(string(name)))
		relative, err := filepath.Rel(sourceRoot, absolute)
		if err != nil {
			result[name] = changes

			continue
		}
		name := FileName(filepath.ToSlash(relative))
		result[name] = append(result[name], changes...)
	}

	return result
}
