package mutator

// ExecutionEvidence describes an observed process attempt, not a replay recipe.
// A nil evidence pointer means no attempt was observed by this implementation.
type ExecutionEvidence struct {
	// Confidence is absent unless bounded outcome confirmation was requested.
	Confidence string `json:"confidence,omitempty"`
	// Attempts contains the original observation followed by bounded confirmation
	// observations. Children have no nested Attempts.
	Attempts []ExecutionEvidence `json:"attempts,omitempty"`
	Role     string              `json:"role,omitempty"`

	Args                  []string      `json:"args"`
	WorkingDirectory      string        `json:"working_directory"`
	PackageScope          string        `json:"package_scope"`
	DurationSeconds       float64       `json:"duration_seconds"`
	TimeoutSeconds        float64       `json:"timeout_seconds"`
	Category              string        `json:"category"`
	ExitCode              *int          `json:"exit_code,omitempty"`
	FailingTests          []FailingTest `json:"failing_tests"`
	PassingTests          []FailingTest `json:"passing_tests,omitempty"`
	PassingTestsTruncated bool          `json:"passing_tests_truncated,omitempty"`
	SelectedPassingTest   *FailingTest  `json:"selected_passing_test,omitempty"`
	FailingTestsTruncated bool          `json:"failing_tests_truncated"`
	Stdout                Diagnostic    `json:"stdout"`
	Stderr                Diagnostic    `json:"stderr"`
	ProcessError          *Diagnostic   `json:"process_error,omitempty"`
	JSONValid             bool          `json:"json_valid"`
}

// FailingTest is an observed Go test fail event; package commands alone do not
// establish individual-test attribution. Subtest names retain their slash path.
type FailingTest struct {
	Package string `json:"package"`
	Test    string `json:"test"`
}

// Diagnostic is a bounded prefix, redacted before publication.
type Diagnostic struct {
	Text            string `json:"text"`
	Truncated       bool   `json:"truncated"`
	RedactionPolicy string `json:"redaction_policy"`
	Redacted        bool   `json:"redacted"`
}

// EvidenceRecorder can be embedded by mutators without changing Mutator.
type EvidenceRecorder struct{ executionEvidence *ExecutionEvidence }

// SetExecutionEvidence records a completed process observation.
func (r *EvidenceRecorder) SetExecutionEvidence(e *ExecutionEvidence) { r.executionEvidence = e }

// ExecutionEvidence returns the observation, or nil when unavailable.
func (r *EvidenceRecorder) ExecutionEvidence() *ExecutionEvidence { return r.executionEvidence }

// ReusableDetection explicitly opts confirmed test failures into future reuse.
// Missing evidence, timeouts and unverified/unstable observations are not reusable.
func (e *ExecutionEvidence) ReusableDetection() bool {
	return e != nil && e.Confidence == "confirmed" && e.Category == "test"
}
