package ledger_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/ledger"
)

func capturedDirectoryEntry(t *testing.T, parent, name string) fs.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return entry
		}
	}
	t.Fatal("directory entry not found")

	return nil
}

func TestDirectoryMetadataWindowsReadsCurrentTimestampNotEnumerationSnapshot(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "directory")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	entry := capturedDirectoryEntry(t, parent, "directory")
	snapshot, err := entry.Info()
	if err != nil {
		t.Fatal(err)
	}
	changed := snapshot.ModTime().Add(-time.Hour)
	for i := 0; i < 2; i++ {
		if err = os.Chtimes(path, changed, changed); err != nil {
			t.Fatal(err)
		}
		cached, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !cached.ModTime().Equal(snapshot.ModTime()) {
			t.Fatal("Windows enumeration entry did not retain its snapshot")
		}
		current, err := ledger.DirectoryMetadata(path, entry)
		if err != nil {
			t.Fatal(err)
		}
		if !current.ModTime().Equal(changed) {
			t.Fatal("directory fingerprint metadata did not read the exact current timestamp")
		}
		if current.ModTime().Equal(cached.ModTime()) {
			t.Fatal("stale enumeration timestamp used as current metadata")
		}
		changed = changed.Add(-time.Hour)
	}
}

func TestDirectoryMetadataWindowsRejectsCachedDirectoryReplacedByJunction(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "directory")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	entry := capturedDirectoryEntry(t, parent, "directory")
	if !entry.IsDir() {
		t.Fatal("expected captured directory entry")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	junction(t, path, t.TempDir())
	if _, err := ledger.DirectoryMetadata(path, entry); err == nil {
		t.Fatal("stale directory entry caused a junction to be accepted")
	}
}

func TestDirectoryMetadataWindowsPreservesLongPathSupport(t *testing.T) {
	parent := t.TempDir()
	for len(parent) < 280 {
		parent = filepath.Join(parent, "directory-segment")
	}
	path := filepath.Join(parent, "directory")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	entry := capturedDirectoryEntry(t, parent, "directory")
	info, err := ledger.DirectoryMetadata(path, entry)
	if err != nil || !info.IsDir() {
		t.Fatal("long directory metadata path unavailable", err)
	}
	if len(path) <= 260 || strings.Contains(path, `\\?\`) {
		t.Fatal("fixture did not exercise an ordinary long DOS path")
	}
}
