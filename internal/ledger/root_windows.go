package ledger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func canonicalRoot(path string) (string, error) {
	// EvalSymlinks does not resolve mount points reported as ModeIrregular by
	// current Go versions. Opening only the selected directory lets Windows
	// resolve its reparse chain; nested entries are still never followed.
	// #nosec G304 -- this is an explicitly selected input root or ledger directory.
	directory, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = directory.Close() }()
	info, err := directory.Stat()
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("input root is not a directory")
	}
	// Windows extended paths are bounded to 32767 UTF-16 code units. Oversized
	// or unavailable final paths fail closed rather than allocating on demand.
	const maxFinalPathUnits = 32768
	buffer := make([]uint16, maxFinalPathUnits)
	// Zero requests FILE_NAME_NORMALIZED | VOLUME_NAME_DOS.
	length, err := windows.GetFinalPathNameByHandle(windows.Handle(directory.Fd()), &buffer[0], maxFinalPathUnits, 0)
	if err != nil {
		return "", err
	}
	if length == 0 || length >= maxFinalPathUnits {
		return "", fmt.Errorf("canonical root path unavailable")
	}
	resolved := windows.UTF16ToString(buffer[:length])
	switch {
	case strings.HasPrefix(resolved, `\\?\UNC\`):
		resolved = `\\` + resolved[len(`\\?\UNC\`):]
	case strings.HasPrefix(resolved, `\\?\`):
		resolved = resolved[len(`\\?\`):]
	default:
		return "", fmt.Errorf("canonical root path has unexpected prefix")
	}
	// Use one DOS/UNC spelling for containment checks. filepath.Rel compares
	// Windows path components case-insensitively, including drive letters.
	volume := filepath.VolumeName(resolved)
	if !filepath.IsAbs(resolved) || (len(volume) != 2 && !strings.HasPrefix(volume, `\\`)) {
		return "", fmt.Errorf("canonical root path is not absolute DOS or UNC")
	}

	return filepath.Clean(resolved), nil
}
