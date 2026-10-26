package configuration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExperimentalConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  string
		env     string
		enabled bool
		invalid bool
	}{
		{name: "disabled by default", config: "{}"},
		{name: "YAML list", config: "experimental: [detailed-report]", enabled: true},
		{name: "empty list", config: "experimental: []"},
		{name: "unknown feature", config: "experimental: [typo]", invalid: true},
		{name: "mixed features rejected", config: "experimental: [detailed-report, typo]", enabled: true, invalid: true},
		{name: "environment", config: "{}", env: "detailed-report", enabled: true},
		{name: "environment overrides YAML", config: "experimental: [typo]", env: "detailed-report", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Reset()
			t.Cleanup(Reset)
			t.Setenv("IMP_EXPERIMENTAL", tc.env)
			path := filepath.Join(t.TempDir(), ".imp.yaml")
			if err := os.WriteFile(path, []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Init([]string{path}); err != nil {
				t.Fatal(err)
			}
			if got := ExperimentalEnabled(ExperimentalDetailedReport); got != tc.enabled {
				t.Errorf("enabled = %v, want %v", got, tc.enabled)
			}
			if err := ValidateExperimental(); (err != nil) != tc.invalid {
				t.Errorf("validation error = %v, invalid = %v", err, tc.invalid)
			}
		})
	}
}
