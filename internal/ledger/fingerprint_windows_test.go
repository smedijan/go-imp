package ledger_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/ledger"
)

func junction(t *testing.T, selected, target string) {
	t.Helper()
	// #nosec G204 -- both junction paths are constructed inside this test's temporary directory.
	command := exec.CommandContext(context.Background(), "cmd", "/c", "mklink", "/J", selected, target)
	if err := command.Run(); err != nil {
		t.Fatalf("create test-owned root junction: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(selected) })
}

func TestFingerprintWindowsJunctionRoot(t *testing.T) {
	testFingerprintSelectedRoot(t, junction)
}

func TestFingerprintWindowsJunctionCacheCaseInsensitiveOverlap(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	root, _ := fixture(t)
	alias := filepath.Join(t.TempDir(), "cache-alias")
	junction(t, alias, root)
	_, err := ledger.Fingerprint(context.Background(), root, filepath.Join(strings.ToUpper(alias), "ledger"), "", "")
	if err == nil || !strings.Contains(err.Error(), "ledger overlaps") {
		t.Fatalf("case-varied canonical cache overlap not rejected: %v", err)
	}
}

func TestFingerprintWindowsRootContainmentGeometry(t *testing.T) {
	for _, tc := range []struct {
		root, path string
		overlap    bool
	}{
		{`C:\tree`, `c:\TREE\ledger`, true},
		{`C:\tree`, `C:\tree`, true},
		{`C:\tree`, `C:\tree-neighbor\ledger`, false},
		{`C:\tree`, `D:\cache\ledger`, false},
		{`C:\tree`, `relative\ledger`, true},
		{`\\server\share\tree`, `\\SERVER\SHARE\TREE\ledger`, true},
		{`\\server\share\tree`, `\\other\share\ledger`, true},
	} {
		if actual := ledger.InputRootOverlapsPath(tc.root, tc.path); actual != tc.overlap {
			t.Fatalf("root=%q path=%q: overlap=%t, want %t", tc.root, tc.path, actual, tc.overlap)
		}
	}
}
