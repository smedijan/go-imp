package mutator

// Identity describes the canonical source edit. IDs are opaque, versioned values.
// Operator aliases are deliberately absent from the identity.
type Identity struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	ModulePath  string `json:"module_path"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
}

// Identified is implemented by source mutants with stable identities.
type Identified interface {
	Identity() Identity
}

// ReplayTarget reports resolution independently of coverage and execution status.
type ReplayTarget struct {
	ID         string `json:"id"`
	Resolution string `json:"resolution"`
}
