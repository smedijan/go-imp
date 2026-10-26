/*
 * Copyright 2022 The Gremlins Authors
 *
 *    Licensed under the Apache License, Version 2.0 (the "License");
 *    you may not use this file except in compliance with the License.
 *    You may obtain a copy of the License at
 *
 *        http://www.apache.org/licenses/LICENSE-2.0
 *
 *    Unless required by applicable law or agreed to in writing, software
 *    distributed under the License is distributed on an "AS IS" BASIS,
 *    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *    See the License for the specific language governing permissions and
 *    limitations under the License.
 */

package configuration_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mitchellh/go-homedir"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		paths     []string
		env       map[string]string
		want      map[string]any
		wantError bool
	}{
		{name: "single file", paths: []string{"testdata/config1/.imp.yaml"}, want: map[string]any{"run.dry-run": true, "run.tags": "tag1,tag2,tag3"}},
		{name: "unreadable file", paths: []string{"testdata/config1/.mutant"}, wantError: true},
		{name: "directory", paths: []string{"testdata/config1"}, want: map[string]any{"run.dry-run": true, "run.tags": "tag1,tag2,tag3"}},
		{name: "first directory wins", paths: []string{"testdata/config2", "testdata/config1"}, want: map[string]any{"run.dry-run": true, "run.tags": "tag1.2,tag2.2,tag3.2"}},
		{name: "environment", env: map[string]string{"IMP_RUN_DRY_RUN": "true", "IMP_RUN_TAGS": "tag1,tag2,tag3"}, want: map[string]any{"run.dry-run": "true", "run.tags": "tag1,tag2,tag3"}},
		{name: "environment overrides file", paths: []string{"testdata/config1"}, env: map[string]string{"IMP_RUN_TAGS": "tag1env,tag2env,tag3env"}, want: map[string]any{"run.dry-run": true, "run.tags": "tag1env,tag2env,tag3env"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			err := configuration.Init(tc.paths)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantError {
				return
			}
			for key, want := range tc.want {
				if got := configuration.Get[any](key); !cmp.Equal(got, want) {
					t.Error(cmp.Diff(want, got))
				}
			}
		})
	}
}

func TestConfigDiscovery(t *testing.T) {
	for _, location := range []string{"module root", "current directory", "XDG config", "home config", "outside module"} {
		t.Run(location, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			xdg := filepath.Join(dir, "xdg")
			module := filepath.Join(dir, "module")
			cwd := filepath.Join(module, "nested")
			for _, path := range []string{home, filepath.Join(xdg, "imp", "imp"), cwd} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			homedir.Reset()
			t.Cleanup(homedir.Reset)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", xdg)
			writeConfigFixture(t, filepath.Join(module, "go.mod"), "module example.com/fixture\n")
			var configDir string
			switch location {
			case "module root":
				configDir = module
			case "current directory":
				configDir = cwd
			case "XDG config":
				configDir = filepath.Join(xdg, "imp", "imp")
			case "home config":
				configDir = filepath.Join(home, ".imp")
				if err := os.MkdirAll(configDir, 0o700); err != nil {
					t.Fatal(err)
				}
			case "outside module":
				cwd = t.TempDir()
				configDir = cwd
			}
			writeConfigFixture(t, filepath.Join(configDir, ".imp.yaml"), "run:\n  tags: discovered\n")
			t.Chdir(cwd)
			if err := configuration.Init(nil); err != nil {
				t.Fatal(err)
			}
			if got := configuration.Get[string](configuration.RunTagsKey); got != "discovered" {
				t.Fatalf("tags=%q, config at %s was not discovered", got, configDir)
			}
		})
	}
}

func writeConfigFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratesMutantTypeEnabledKey(t *testing.T) {
	if got := configuration.MutantTypeEnabledKey(mutator.ArithmeticBase); got != "mutants.arithmetic-base.enabled" {
		t.Errorf("unexpected key: %s", got)
	}
}

func TestViperSynchronisedAccess(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"bool", true}, {"int", 10}, {"float64", float64(10)}, {"string", "test string"}, {"char", 'a'},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := "tvsa." + tc.name + ".key"
			configuration.Set(key, tc.value)
			if got := configuration.Get[any](key); !cmp.Equal(got, tc.value) {
				t.Error(cmp.Diff(tc.value, got))
			}
		})
	}
}

func TestReset(t *testing.T) {
	configuration.Set("test.key", true)
	configuration.Reset()
	if configuration.Get[bool]("test.key") {
		t.Error("expected config to be reset")
	}
}
