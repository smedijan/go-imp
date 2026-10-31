//go:build !windows

package ledger_test

import (
	"os"
	"testing"
)

func TestFingerprintSymlinkRoot(t *testing.T) {
	testFingerprintSelectedRoot(t, func(t *testing.T, selected, target string) {
		t.Helper()
		if err := os.Symlink(target, selected); err != nil {
			t.Fatal(err)
		}
	})
}
