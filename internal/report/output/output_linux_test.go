package output_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/smedijan/go-imp/internal/report/output"
)

func TestReportRejectsDevicesAtOpen(t *testing.T) {
	for _, path := range []string{"/dev/null", "/dev/zero"} {
		t.Run(path, func(t *testing.T) {
			destination, err := output.Open(io.Discard, path)
			if destination != nil {
				_ = destination.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "not a regular file") {
				t.Fatalf("device must be rejected before staging: %v", err)
			}
		})
	}
}

func TestReportRejectsSpecialDestinations(t *testing.T) {
	for _, kind := range []string{"FIFO", "symlink", "dangling symlink", "device"} {
		for _, beforeOpen := range []bool{false, true} {
			name := kind + "/" + map[bool]string{false: "replaced before commit", true: "present at open"}[beforeOpen]
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "report")
				var destination *output.Destination
				var err error
				if !beforeOpen {
					destination, err = output.Open(io.Discard, path)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = destination.Close() })
				}
				switch kind {
				case "FIFO":
					if err := syscall.Mkfifo(path, 0o600); err != nil {
						t.Fatal(err)
					}
				case "symlink", "dangling symlink":
					target := filepath.Join(dir, "target")
					if kind == "symlink" {
						writeFixture(t, target, "keep target unchanged")
					}
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				case "device":
					// A symlink to a device exercises rejection without needing root to mknod.
					if err := os.Symlink("/dev/null", path); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				if beforeOpen {
					destination, err = output.Open(io.Discard, path)
				} else {
					err = destination.Commit()
				}
				if err == nil {
					_ = destination.Close()
					t.Fatal("special destination was accepted")
				}
				after, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				if before.Mode() != after.Mode() {
					t.Fatalf("destination file type changed from %s to %s", before.Mode(), after.Mode())
				}
				if destination != nil {
					_ = destination.Close()
				}
				assertNoTemporaryReports(t, dir)
			})
		}
	}
}
