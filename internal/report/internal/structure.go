// Package internal provides the versioned JSON report schema.
package internal

import (
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/triage"
)

// OutputResult describes the whole run; Groups contains only selected findings.
type OutputResult struct {
	Triage            *TriageAudit                     `json:"triage,omitempty"`
	ReviewGroups      []ReviewGroup                    `json:"review_groups,omitempty"`
	Selection         *diff.Selection                  `json:"selection,omitempty"`
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

// ReviewGroup is a bounded review task, not a claim of shared root cause.
// IDs refer to the underlying findings; grouping never replaces them.
type ReviewGroup struct {
	ID        string   `json:"id"`
	Criterion string   `json:"criterion"`
	MutantIDs []string `json:"mutant_ids"`
}

// TriageAudit retains all dispositions even when reviewed findings are hidden.
type TriageAudit struct {
	Ledger       triage.Ledger  `json:"ledger"`
	Counts       map[string]int `json:"disposition_counts"`
	Reviewed     int            `json:"reviewed"`
	Hidden       int            `json:"hidden"`
	HideReviewed bool           `json:"hide_reviewed"`
	GatePolicy   string         `json:"gate_policy"`
}
