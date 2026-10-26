package configuration_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
)

func TestTypedEnvironmentSettings(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	t.Setenv("IMP_RUN_JSON", "true")
	t.Setenv("IMP_RUN_WORKERS", "3")
	t.Setenv("IMP_RUN_THRESHOLD_EFFICACY", "82.5")
	t.Setenv("IMP_RUN_STATUSES", "lived,not-covered")
	if err := configuration.Init([]string{t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if !configuration.Get[bool](configuration.RunJSONKey) || configuration.Get[int](configuration.RunWorkersKey) != 3 || configuration.Get[float64](configuration.RunThresholdEfficacyKey) != 82.5 ||
		configuration.Get[string](configuration.RunStatusesKey) != "lived,not-covered" {
		t.Fatal("environment settings were not converted to their declared types")
	}
}

func TestMalformedDiscoveredConfiguration(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".imp.yaml"), []byte("run: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := configuration.Init([]string{dir}); err == nil {
		t.Fatal("invalid discovered YAML must not be silently ignored")
	}
}
