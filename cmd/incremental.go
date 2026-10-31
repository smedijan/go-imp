package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/ledger"
	"github.com/smedijan/go-imp/internal/mutator"
)

func incrementalSession(ctx context.Context, mod gomodule.GoModule) *ledger.Session {
	if !configuration.Get[bool](configuration.RunIncrementalKey) || configuration.Get[bool](configuration.RunDryRunKey) {
		return nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(cache, "imp", "outcomes-v1", fmt.Sprintf("%x.json", sha256.Sum256([]byte(mod.Root))))
	settings := map[string]any{"calling-dir": mod.CallingDir, "cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0)}
	for _, key := range []string{configuration.RunTagsKey, configuration.RunCoverPkgKey, configuration.RunDiffRef, configuration.RunDiffScope} {
		settings[key] = configuration.Get[string](key)
	}
	for _, key := range []string{configuration.RunTestCPUKey, configuration.RunTimeoutCoefficientKey, configuration.RunWorkersKey} {
		settings[key] = configuration.Get[int](key)
	}
	settings[configuration.RunIntegrationMode] = configuration.Get[bool](configuration.RunIntegrationMode)
	settings[configuration.RunConfirmOutcomesKey] = configuration.Get[bool](configuration.RunConfirmOutcomesKey)
	for _, key := range []string{configuration.RunExcludeFiles, configuration.RunIncludeFiles, configuration.RunExcludeWorkdir} {
		settings[key] = configuration.Get[[]string](key)
	}
	for _, mt := range mutator.Types {
		key := configuration.MutantTypeEnabledKey(mt)
		settings[key] = configuration.Get[bool](key)
	}
	encoded, _ := json.Marshal(settings)

	return ledger.Open(ctx, ledger.Options{
		Path: path, Refresh: configuration.Get[bool](configuration.RunIncrementalRefreshKey),
		TTL:        time.Duration(configuration.Get[int](configuration.RunIncrementalTTLKey)) * time.Hour,
		MaxEntries: configuration.Get[int](configuration.RunIncrementalEntriesKey),
		Fingerprint: func(ctx context.Context) (string, error) {
			return ledger.Fingerprint(ctx, mod.Root, path, string(encoded), configuration.Get[string](configuration.RunTagsKey))
		},
	})
}

func validateIncrementalOptions() error {
	enabled := configuration.Get[bool](configuration.RunIncrementalKey)
	if configuration.Get[bool](configuration.RunIncrementalRefreshKey) && !enabled {
		return fmt.Errorf("--incremental-refresh requires --incremental")
	}
	if !enabled {
		return nil
	}
	hours := configuration.Get[int](configuration.RunIncrementalTTLKey)
	entries := configuration.Get[int](configuration.RunIncrementalEntriesKey)
	if hours < 1 || hours > 720 || entries < 1 || entries > 10000 {
		return fmt.Errorf("incremental retention requires 1..720 hours and 1..10000 entries")
	}

	return nil
}
