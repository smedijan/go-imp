// Package output publishes reports to streams or atomically replaced files.
package output

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Destination stages file reports next to the destination. Until Commit succeeds,
// failures and cancellation leave any previous report untouched.
// Close discards any uncommitted report and never closes a caller-owned stream.
type Destination struct {
	io.Writer
	file      *os.File
	temporary string
	target    string
}

// Open binds path to the current directory. Empty paths and "-" use stdout.
func Open(stdout io.Writer, path string) (*Destination, error) {
	if path == "" || path == "-" {
		return &Destination{Writer: stdout}, nil
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve report %q: %w", path, err)
	}
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".imp-report-*")
	if err != nil {
		return nil, fmt.Errorf("open report %q: %w", path, err)
	}

	return &Destination{Writer: file, file: file, temporary: file.Name(), target: target}, nil
}

func validateTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect report %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("report destination %q is not a regular file; use --output - and shell redirection for pipes or devices", path)
	}

	return nil
}

// Commit publishes the complete report, replacing the previous regular file.
func (out *Destination) Commit() error {
	if out.temporary == "" {
		return nil
	}
	if out.file != nil {
		err := out.file.Close()
		out.file = nil
		if err != nil {
			return fmt.Errorf("close report: %w", err)
		}
	}
	if err := validateTarget(out.target); err != nil {
		return err
	}
	if err := os.Rename(out.temporary, out.target); err != nil {
		return fmt.Errorf("replace report: %w", err)
	}
	out.temporary = ""

	return nil
}

// Close releases resources and removes any uncommitted report. It is idempotent.
func (out *Destination) Close() error {
	var closeErr, removeErr error
	if out.file != nil {
		closeErr = out.file.Close()
		out.file = nil
	}
	if out.temporary != "" {
		removeErr = os.Remove(out.temporary)
		out.temporary = ""
	}

	return errors.Join(closeErr, removeErr)
}

// Distinct rejects lexical, symlink and hardlink aliases among input and output
// files. Call before opening any destination. Empty paths are ignored; callers
// must replace stdout sentinels with empty paths. A literal "-" is a filename.
func Distinct(paths ...string) error {
	for i, path := range paths {
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		info, statErr := os.Stat(path)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		resolved, resolveErr := filepath.EvalSymlinks(absolute)
		if resolveErr != nil {
			resolved = resolveParent(absolute)
		}
		for _, other := range paths[:i] {
			if other == "" {
				continue
			}
			otherAbs, err := filepath.Abs(other)
			if err != nil {
				return err
			}
			otherResolved, err := filepath.EvalSymlinks(otherAbs)
			if err != nil {
				otherResolved = resolveParent(otherAbs)
			}
			otherInfo, _ := os.Stat(other)
			if absolute == otherAbs || resolved == otherResolved || (info != nil && otherInfo != nil && os.SameFile(info, otherInfo)) {
				return fmt.Errorf("input/output paths must be distinct: %q aliases %q", path, other)
			}
		}
	}

	return nil
}

func resolveParent(path string) string {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return path
	}

	return filepath.Join(parent, filepath.Base(path))
}
