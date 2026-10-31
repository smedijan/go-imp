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

// Package configuration manages application settings from flags, config files, and environment variables.
package configuration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"

	"github.com/smedijan/go-imp/internal/mutator"
)

// This is the list of the keys available in config files and as flags.
const (
	ImpQuietKey              = "quiet"
	ImpNoColorKey            = "no-color"
	RunMutantIDsKey          = "run.mutant-ids"
	RunReplayReportKey       = "run.replay-report"
	RunJSONKey               = "run.json"
	RunVerboseKey            = "run.verbose"
	RunConfirmOutcomesKey    = "run.confirm-outcomes"
	RunDryRunKey             = "run.dry-run"
	RunStatusesKey           = "run.statuses"
	RunOutputKey             = "run.output"
	RunTagsKey               = "run.tags"
	RunCoverPkgKey           = "run.coverpkg"
	RunWorkersKey            = "run.workers"
	RunTestCPUKey            = "run.test-cpu"
	RunTimeoutCoefficientKey = "run.timeout-coefficient"
	RunIntegrationMode       = "run.integration"
	RunExcludeFiles          = "run.exclude-files"
	RunIncludeFiles          = "run.include-files"
	RunExcludeWorkdir        = "run.exclude-workdir"
	RunDiffRef               = "run.diff"
	RunDiffScope             = "run.diff-scope"
	RunThresholdEfficacyKey  = "run.threshold.efficacy"
	RunThresholdMCoverageKey = "run.threshold.mutant-coverage"
)

const (
	impCfgName      = ".imp"
	impEnvVarPrefix = "IMP"

	xdgConfigHomeKey = "XDG_CONFIG_HOME"

	windowsOs = "windows"
)

// Init initializes the viper configuration for imp.
//
// It sets the configuration file name as .imp.yaml, adds the passed paths as ConfigPaths
// AutomaticEnv configuration having IMP as prefix.
// The environment variables take precedence over the configuration file and must be set in the
// format:
//
//	IMP_<COMMAND NAME>_<FLAG NAME>
func Init(cPaths []string) error {
	replacer := strings.NewReplacer(".", "_", "-", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.SetEnvPrefix(impEnvVarPrefix)
	viper.AutomaticEnv()
	viper.SetConfigName(impCfgName)
	viper.SetConfigType("yaml")

	if isSpecificFile(cPaths) {
		viper.SetConfigFile(cPaths[0])

		return viper.ReadInConfig()
	} else if arePathsNotSet(cPaths) {
		cPaths = defaultConfigPaths()
	}

	for _, p := range cPaths {
		viper.AddConfigPath(p)
	}

	var missing viper.ConfigFileNotFoundError
	if err := viper.ReadInConfig(); err != nil && !errors.As(err, &missing) {
		return err
	}

	return nil
}

// MutantTypeEnabledKey returns the configuration key for a mutant.
// The generated key will have the format 'mutants.mutant-name.enabled",
// which corresponds to the Yaml:
//
//		mutants:
//	 		mutant-name:
//	 			enabled: [bool]
func MutantTypeEnabledKey(mt mutator.Type) string {
	m := mt.String()
	m = strings.ReplaceAll(m, "_", "-")
	m = strings.ToLower(m)

	return fmt.Sprintf("mutants.%s.enabled", m)
}

func isSpecificFile(cPaths []string) bool {
	return len(cPaths) == 1 && filepath.Ext(cPaths[0]) != ""
}

func arePathsNotSet(cPaths []string) bool {
	return len(cPaths) == 0 || len(cPaths) == 1 && cPaths[0] == ""
}

func defaultConfigPaths() []string {
	result := make([]string, 0, 4)

	// First global config
	if runtime.GOOS != windowsOs {
		result = append(result, "/etc/imp")
	}

	// Then $XDG_CONFIG_HOME
	xchLocation, _ := homedir.Expand("~/.config")
	if x := os.Getenv(xdgConfigHomeKey); x != "" {
		xchLocation = x
	}
	xchLocation = filepath.Join(xchLocation, "imp", "imp")
	result = append(result, xchLocation)

	// Then $HOME
	homeLocation, err := homedir.Expand("~/.imp")
	if err != nil {
		return result
	}
	result = append(result, homeLocation)

	// Then the Go module root
	if root := findModuleRoot(); root != "" {
		result = append(result, root)
	}

	// Finally the current directory
	result = append(result, ".")

	return result
}

func findModuleRoot() string {
	// This function is duplicated from internal/gomodule. We should find a way
	// to use here gomodule. The problem is the point of initialization, because
	// configuration is initialised before gomodule.
	path, _ := os.Getwd()
	for {
		if fi, err := os.Stat(filepath.Join(path, "go.mod")); err == nil && !fi.IsDir() {
			return path
		}
		d := filepath.Dir(path)
		if d == path {
			break
		}
		path = d
	}

	return ""
}

var mutex sync.RWMutex

// Set offers synchronised access to Viper.
func Set[T any](k string, v T) {
	mutex.Lock()
	defer mutex.Unlock()
	viper.Set(k, v)
}

// Get offers synchronised access to Viper.
func Get[T any](k string) T {
	var r T
	mutex.RLock()
	defer mutex.RUnlock()
	var value any
	switch any(r).(type) {
	case bool:
		value = viper.GetBool(k)
	case int:
		value = viper.GetInt(k)
	case float64:
		value = viper.GetFloat64(k)
	case string:
		value = viper.GetString(k)
	case []string:
		value = viper.GetStringSlice(k)
	default:
		value = viper.Get(k)
	}
	r, _ = value.(T)

	return r
}

// Reset is used mainly for testing purposes, in order to clean up the Viper
// instance.
func Reset() {
	mutex.Lock()
	defer mutex.Unlock()
	viper.Reset()
}
