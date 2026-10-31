package report

import (
	"encoding/json"
	"io"
)

// Failure is a pre-final error/partial-state envelope, emitted only to stderr.
// No partial findings are currently published; the previous committed file is
// never replaced with this envelope. Arbitrary error text is deliberately absent.
type Failure struct {
	SchemaVersion           int    `json:"schema_version"`
	Kind                    string `json:"kind"`
	Stage                   string `json:"stage"`
	ReportState             string `json:"report_state"`
	PartialResultsAvailable bool   `json:"partial_results_available"`
	Cancelled               bool   `json:"cancelled"`
	DiagnosticsPolicy       string `json:"diagnostics_policy"`
}

// WriteFailure appends one identifiable JSON line to the diagnostic stream.
func WriteFailure(stderr io.Writer, stage string, cancelled bool) error {
	return json.NewEncoder(stderr).Encode(Failure{SchemaVersion: 2, Kind: "run_error", Stage: stage,
		ReportState: "not-committed", Cancelled: cancelled,
		DiagnosticsPolicy: "error-text-omitted; inspect-human-stderr"})
}
