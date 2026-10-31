package ledger_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/ledger"
)

func testFingerprintSelectedRoot(t *testing.T, link func(*testing.T, string, string)) {
	t.Helper()
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	root, _ := fixture(t)
	selected := filepath.Join(filepath.Dir(root), "selected")
	link(t, selected, root)
	path := filepath.Join(t.TempDir(), "ledger")
	fingerprint := func(root string) (string, error) { return ledger.Fingerprint(context.Background(), root, path, "", "") }
	original, err := fingerprint(selected)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := fingerprint(selected); err != nil || again != original {
		t.Fatal("unstable resolved root", err)
	}
	if direct, err := fingerprint(root); err != nil || direct == original {
		t.Fatal("root selection identity missing", err)
	}
	fixturePath := filepath.Join(root, "data.txt")
	info, err := os.Stat(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	write(t, fixturePath, "modified embed input")
	if modified, err := fingerprint(selected); err != nil || modified == original {
		t.Fatal("canonical target content not hashed", err)
	}
	write(t, fixturePath, "embedded")
	if err = os.Chtimes(fixturePath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	// Identical file bytes and timestamps at a different canonical root must not
	// share evidence after the selected junction/symlink is retargeted.
	other := filepath.Join(filepath.Dir(root), "other")
	if err = os.Rename(root, other); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(selected); err != nil {
		t.Fatal(err)
	}
	link(t, selected, other)
	if retargeted, err := fingerprint(selected); err != nil || retargeted == original {
		t.Fatal("root retarget did not invalidate", err)
	}
	nested := filepath.Join(other, "nested")
	link(t, nested, filepath.Dir(other))
	if _, err = fingerprint(selected); err == nil || !strings.Contains(err.Error(), `"nested"`) {
		t.Fatalf("nested link not rejected: %v", err)
	}
	if err = os.Remove(nested); err != nil {
		t.Fatal(err)
	}
	// Resolve the cache directory as well: a lexically external cache alias can
	// still point into the canonical input tree.
	cacheAlias := filepath.Join(filepath.Dir(other), "cache-alias")
	link(t, cacheAlias, other)
	if _, err = ledger.Fingerprint(context.Background(), selected, filepath.Join(cacheAlias, "ledger"), "", ""); err == nil || !strings.Contains(err.Error(), "ledger overlaps") {
		t.Fatalf("resolved ledger overlap not rejected: %v", err)
	}
}
