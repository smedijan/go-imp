package output_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/internal/report/output"
)

func TestDistinctProtectsTriageAliases(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "triage.json")
	if err := os.WriteFile(input, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := output.Distinct(input, input); err == nil {
		t.Fatal("same path accepted")
	}
	for _, link := range []struct {
		name   string
		create func(string, string) error
	}{{"symlink", os.Symlink}, {"hardlink", os.Link}} {
		t.Run(link.name, func(t *testing.T) {
			path := filepath.Join(dir, link.name)
			if err := link.create(input, path); err != nil {
				t.Skip(err)
			}
			if err := output.Distinct(input, path); err == nil {
				t.Fatal("alias accepted")
			}
		})
	}
	if err := output.Distinct(input, filepath.Join(dir, "new.json"), "-"); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, aliasDir); err == nil {
		if err := output.Distinct(filepath.Join(dir, "absent"), filepath.Join(aliasDir, "absent")); err == nil {
			t.Fatal("absent targets with symlink parent accepted")
		}
	}
	// #nosec G304 -- test-owned fixture path.
	data, err := os.ReadFile(input)
	if err != nil || string(data) != "original" {
		t.Fatal("input modified")
	}
}
