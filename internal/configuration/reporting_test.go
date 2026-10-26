package configuration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTypedEnvironmentSettings(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	t.Setenv("IMP_RUN_JSON", "true")
	t.Setenv("IMP_RUN_WORKERS", "3")
	t.Setenv("IMP_RUN_THRESHOLD_EFFICACY", "82.5")
	t.Setenv("IMP_RUN_STATUSES", "lived,not-covered")
	if err := Init([]string{t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if !Get[bool](RunJSONKey) || Get[int](RunWorkersKey) != 3 || Get[float64](RunThresholdEfficacyKey) != 82.5 ||
		Get[string](RunStatusesKey) != "lived,not-covered" {
		t.Fatal("environment settings were not converted to their declared types")
	}
}

func TestMalformedDiscoveredConfiguration(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".imp.yaml"), []byte("run: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Init([]string{dir}); err == nil {
		t.Fatal("invalid discovered YAML must not be silently ignored")
	}
}
