package ledger_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestFingerprintWindowsToolchainInputMetadata narrows hosted Windows failures
// without treating unknown reparse points as regular files. Diagnostics contain
// only root-relative paths and numeric metadata, never environment/root values.
func TestFingerprintWindowsToolchainInputMetadata(t *testing.T) {
	output, err := exec.CommandContext(context.Background(), "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("Go toolchain lookup failed (%T)", err)
	}
	root := strings.TrimSpace(string(output))
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			t.Fatalf("toolchain enumeration failed (%T)", walkErr)
		}
		if entry.IsDir() || entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative toolchain path unavailable (%T)", err)
		}
		relative = filepath.ToSlash(relative)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("input %q: fresh Lstat failed (%T)", relative, err)
		}
		name, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			t.Fatalf("input %q: Windows path conversion failed (%T)", relative, err)
		}
		var data syscall.Win32finddata
		handle, err := syscall.FindFirstFile(name, &data)
		if err != nil {
			t.Fatalf("input %q: FindFirstFile failed: %v", relative, err)
		}
		if err = syscall.FindClose(handle); err != nil {
			t.Fatalf("input %q: FindClose failed: %v", relative, err)
		}
		t.Errorf("unsupported toolchain input %q: directory-entry mode=%s fresh-Lstat mode=%s attributes=%#x reparse-tag=%#x", relative, entry.Type(), info.Mode(), data.FileAttributes, data.Reserved0)
		// The first unsupported entry suffices to classify the current failure.
		return fs.SkipAll
	})
	if err != nil {
		t.Fatalf("toolchain enumeration failed (%T)", err)
	}
}
