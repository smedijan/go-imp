package report

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"

	"github.com/smedijan/go-imp/internal/report/internal"
)

var mutantIDPattern = regexp.MustCompile(`^imp-v1:[0-9a-f]{64}:[0-9a-f]{64}$`)

// ValidateMutantIDs rejects unsupported identity versions and malformed IDs.
func ValidateMutantIDs(ids []string) error {
	for _, id := range ids {
		if !mutantIDPattern.MatchString(id) {
			return fmt.Errorf("invalid or unsupported mutant ID %q", id)
		}
	}

	return nil
}

// ReplayIDs reads exactly the findings present in a schema 1 or 2 identity-aware report.
// It never falls back to legacy line/column/operator matching.
func ReplayIDs(reader io.Reader, module string) ([]string, error) {
	var document internal.OutputResult
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("read replay report: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("replay report must contain one JSON document")
	}
	if (document.SchemaVersion != 1 && document.SchemaVersion != 2) || document.IdentityVersion != 1 {
		return nil, fmt.Errorf("replay requires schema_version 1 or 2 and identity_version 1")
	}
	if document.GoModule != module {
		return nil, fmt.Errorf("replay report module %q differs from %q", document.GoModule, module)
	}
	var ids []string
	for _, files := range document.Groups {
		for _, findings := range files {
			for _, finding := range findings {
				if finding.Identity == nil || finding.Identity.Version != 1 || finding.ID != finding.Identity.ID {
					return nil, fmt.Errorf("replay finding lacks a consistent v1 identity")
				}
				ids = append(ids, finding.ID)
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("replay report contains no findings")
	}
	if err := ValidateMutantIDs(ids); err != nil {
		return nil, err
	}
	slices.Sort(ids)

	return ids, nil
}
