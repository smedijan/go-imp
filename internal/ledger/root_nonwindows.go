//go:build !windows

package ledger

import (
	"fmt"
	"os"
	"path/filepath"
)

func canonicalRoot(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("input root is not a directory")
	}

	return resolved, nil
}
