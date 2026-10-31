// Package internal provides the versioned JSON report schema.
package internal

import (
	"github.com/smedijan/go-imp/internal/ledger"
	"github.com/smedijan/go-imp/internal/mutator"
)

// OutputResult describes the whole run; Groups contains only selected findings.
type OutputResult struct {
	Evidence          []ledger.Evidence                `json:"evidence,omitempty"`
	LedgerNote        string                           `json:"ledger_note,omitempty"`
	IdentityVersion   int                              `json:"identity_version"`
	Replay            []mutator.ReplayTarget           `json:"replay,omitempty"`
	SchemaVersion     int                              `json:"schema_version"`
	GoModule          string                           `json:"go_module"`
	DryRun            bool                             `json:"dry_run"`
	SelectedStatuses  []string                         `json:"selected_statuses"`
	Groups            map[string]map[string][]Mutation `json:"groups"`
	TestEfficacy      float64                          `json:"test_efficacy"`
	MutationsCoverage float64                          `json:"mutations_coverage"`
	MutantsTotal      int                              `json:"mutants_total"`
	MutantsKilled     int                              `json:"mutants_killed"`
	MutantsLived      int                              `json:"mutants_lived"`
	MutantsNotViable  int                              `json:"mutants_not_viable"`
	MutantsNotCovered int                              `json:"mutants_not_covered"`
	MutantsTimedOut   int                              `json:"mutants_timed_out"`
	MutantsSkipped    int                              `json:"mutants_skipped"`
	MutantsRunnable   int                              `json:"mutants_runnable"`
	ElapsedTime       float64                          `json:"elapsed_time"`
	MutatorStatistics map[string]int                   `json:"mutator_statistics"`
}

// Mutation omits the status and filename supplied by its enclosing group.
type Mutation struct {
	ObservationContext *mutator.ObservationContext `json:"observation_context"`
	Execution          *mutator.ExecutionEvidence  `json:"execution,omitempty"`
	ID                 string                      `json:"id,omitempty"`
	Identity           *mutator.Identity           `json:"identity,omitempty"`
	AppliedSourceHash  string                      `json:"applied_source_sha256,omitempty"`
	Applied            bool                        `json:"applied"`
	OriginalSnippet    string                      `json:"original_snippet,omitempty"`
	MutatedSnippet     string                      `json:"mutated_snippet,omitempty"`
	Type               string                      `json:"type"`
	Line               int                         `json:"line"`
	Column             int                         `json:"column"`
	TestCommand        string                      `json:"test_command,omitempty"`
}
