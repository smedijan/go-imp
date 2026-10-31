package mutator

// ObservedName is a bounded source label. Known distinguishes missing metadata
// from an empty label; Truncated means Value is only a prefix, not an identity.
type ObservedName struct {
	Value     string `json:"value"`
	Known     bool   `json:"known"`
	Truncated bool   `json:"truncated"`
}

// SelectionObservation records engine decisions, not inferred test impact.
// EligibilityReason describes scheduling eligibility before execution outcomes.
type SelectionObservation struct {
	Discovery         string `json:"discovery"`
	File              string `json:"file"`
	Coverage          string `json:"coverage"`
	Diff              string `json:"diff"`
	Replay            string `json:"replay"`
	DryRun            bool   `json:"dry_run"`
	EligibilityReason string `json:"eligibility_reason"`
	ImpactProvenance  string `json:"impact_provenance"`
}

// ObservationContext describes source ownership and observed selection decisions.
// It does not alter canonical identity or imply dependency/impact analysis.
type ObservationContext struct {
	Package         ObservedName         `json:"package"`
	PackagePath     ObservedName         `json:"package_path"`
	EnclosingSymbol ObservedName         `json:"enclosing_symbol"`
	Selection       SelectionObservation `json:"selection"`
}

// ObservationProvider is the optional source/selection observation seam.
type ObservationProvider interface {
	ObservationContext() *ObservationContext
}

// ObservationRecorder adds optional discovery context without changing Mutator
// or the independent execution-evidence recorder.
type ObservationRecorder struct{ observationContext *ObservationContext }

// SetObservationContext records source and eligibility observations.
func (r *ObservationRecorder) SetObservationContext(c *ObservationContext) { r.observationContext = c }

// ObservationContext returns nil when source/selection observations are unavailable.
func (r *ObservationRecorder) ObservationContext() *ObservationContext { return r.observationContext }
