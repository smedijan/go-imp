package cmd_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/execution"
)

type replayDocument struct {
	Total  int `json:"mutants_total"`
	Killed int `json:"mutants_killed"`
	Replay []struct {
		ID         string `json:"id"`
		Resolution string `json:"resolution"`
	} `json:"replay"`
	Groups map[string]map[string][]struct {
		ID      string `json:"id"`
		Applied bool   `json:"applied"`
	} `json:"groups"`
}

func TestCLIExactReplaySelectorsAndErrors(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	dir := newModuleFixture(t)
	t.Chdir(dir)
	prior, err := executeCLI(context.Background(), t, "test", "run", "--json", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var initial replayDocument
	if err := json.Unmarshal([]byte(prior), &initial); err != nil {
		t.Fatal(err)
	}
	id := initial.Groups["RUNNABLE"]["code.go"][0].ID
	if id == "" {
		t.Fatal("missing discovery ID")
	}
	reportPath := filepath.Join(dir, "prior.json")
	writeFixture(t, reportPath, prior)
	malformed := filepath.Join(dir, "malformed.json")
	writeFixture(t, malformed, "{broken")
	missingID := "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		name            string
		args            []string
		wantErr         string
		usage, executed bool
		total           int
	}{
		{name: "ID", args: []string{"--mutant-ids", id}, total: 1},
		{name: "report", args: []string{"--replay-report", reportPath}, total: 1},
		{name: "union deduplicates", args: []string{"--mutant-ids", id + "," + id, "--mutant-ids", id, "--replay-report", reportPath}, total: 1},
		{name: "executed ID", args: []string{"--mutant-ids", id}, executed: true, total: 1},
		{name: "missing exact target", args: []string{"--mutant-ids", missingID}, wantErr: "exact replay unresolved"},
		{name: "atomic set", args: []string{"--mutant-ids", id + "," + missingID}, wantErr: "exact replay unresolved"},
		{name: "invalid ID", args: []string{"--mutant-ids", "bad-id"}, wantErr: "invalid or unsupported mutant ID", usage: true},
		{name: "missing report", args: []string{"--replay-report", filepath.Join(dir, "absent.json")}, wantErr: "open replay report", usage: true},
		{name: "invalid report", args: []string{"--replay-report", malformed}, wantErr: "read replay report", usage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			args := []string{"run", "--json"}
			if !tc.executed {
				args = append(args, "--dry-run")
			}
			args = append(args, tc.args...)
			out, err := executeCLI(context.Background(), t, "test", args...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want %q, got %v", tc.wantErr, err)
				}
				var usage *execution.UsageError
				if errors.As(err, &usage) != tc.usage {
					t.Fatalf("wrong usage classification: %v", err)
				}
				if tc.usage {
					if out != "" {
						t.Fatalf("invalid selector emitted report: %s", out)
					}

					return
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var result replayDocument
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.Total != tc.total {
				t.Fatalf("total=%d, want %d", result.Total, tc.total)
			}
			if tc.executed && result.Killed != 1 {
				t.Fatalf("replay did not execute mutant: %+v", result)
			}
			if tc.wantErr == "" && (len(result.Replay) != 1 || result.Replay[0].ID != id || result.Replay[0].Resolution != "matched") {
				t.Fatalf("wrong exact selector resolution: %+v", result)
			}
			if tc.wantErr != "" && len(result.Replay) == 0 {
				t.Fatal("missing unresolved diagnostics")
			}
		})
	}
}
