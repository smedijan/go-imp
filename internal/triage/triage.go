// Package triage records human review decisions, never execution or equivalence judgments.
package triage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/smedijan/go-imp/internal/mutator"
)

// Entry applies only to one exact canonical source edit. Resolution is recomputed
// on every run, including when a previously annotated ledger is read back.
type Entry struct {
	Identity    mutator.Identity `json:"identity"`
	Disposition string           `json:"disposition"`
	Reason      string           `json:"reason"`
	Provenance  string           `json:"provenance"`
	Resolution  string           `json:"resolution,omitempty"`
}

// Ledger is a versioned, module-scoped, human-authored review record.
type Ledger struct {
	Version int     `json:"version"`
	Module  string  `json:"module"`
	Entries []Entry `json:"entries"`
}

const matched = "matched"

// Schema keys use only lowercase ASCII letters and underscores. Enforce that
// before decoding so encoding/json cannot accept case-folded field aliases.
var canonicalKeyPattern = regexp.MustCompile(`^[a-z_]+$`)

var idPattern = regexp.MustCompile(`^imp-v1:[0-9a-f]{64}:[0-9a-f]{64}$`)

// Validate rejects unknown decisions, malformed identities, and duplicate anchors.
func (l Ledger) Validate(module string) error {
	if l.Version != 1 || l.Module == "" || l.Module != module || l.Entries == nil {
		return fmt.Errorf("triage requires version 1 and module %q", module)
	}
	seen := make(map[string]bool)
	for _, e := range l.Entries {
		id := e.Identity
		if id.Version != 1 || !idPattern.MatchString(id.ID) || strings.TrimSpace(id.ModulePath) == "" || path.IsAbs(id.ModulePath) || path.Clean(id.ModulePath) != id.ModulePath || id.ModulePath == "." || strings.HasPrefix(id.ModulePath, "../") || strings.Contains(id.ModulePath, `\`) || id.Original == id.Replacement {
			return fmt.Errorf("invalid triage identity %q", id.ID)
		}
		if seen[id.ID] {
			return fmt.Errorf("duplicate triage identity %q", id.ID)
		}
		seen[id.ID] = true
		switch e.Disposition {
		case "intentional-behavior", "suspected-equivalence", "accepted-gap", "needs-clarification":
		default:
			return fmt.Errorf("unknown triage disposition %q", e.Disposition)
		}
		if strings.TrimSpace(e.Reason) == "" || strings.TrimSpace(e.Provenance) == "" {
			return fmt.Errorf("triage %q requires reason and provenance", id.ID)
		}
		switch e.Resolution {
		case "", matched, "stale", "unmatched", "ambiguous":
		default:
			return fmt.Errorf("unknown triage resolution %q", e.Resolution)
		}
	}

	return nil
}

// Read accepts exactly one strict JSON ledger. Unknown fields never become waivers.
func Read(r io.Reader, module string) (*Ledger, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read triage: %w", err)
	}
	if err := uniqueJSONKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return nil, err
	}
	var ledger Ledger
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ledger); err != nil {
		return nil, fmt.Errorf("read triage: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("triage must contain one JSON document")
	}
	if err := ledger.Validate(module); err != nil {
		return nil, err
	}

	return &ledger, nil
}

// Resolve copies the ledger and matches only unique exact identities. Absence is
// deliberately not evidence of change: exclusions/disabled families/replay may
// omit an unchanged anchor. All nonmatched entries require human review.
func Resolve(ledger Ledger, mutants []mutator.Mutator) Ledger {
	result := ledger
	result.Entries = append([]Entry{}, ledger.Entries...)
	byID := make(map[string][]mutator.Identity)
	for _, m := range mutants {
		if p, ok := m.(mutator.Identified); ok {
			id := p.Identity()
			byID[id.ID] = append(byID[id.ID], id)
		}
	}
	for i := range result.Entries {
		e := &result.Entries[i]
		candidates := byID[e.Identity.ID]
		switch len(candidates) {
		case 0:
			e.Resolution = "unmatched"
		case 1:
			e.Resolution = "stale"
			if e.Identity == candidates[0] {
				e.Resolution = matched
			}
		default:
			e.Resolution = "ambiguous"
		}
	}

	return result
}

// Reviewed returns true only for resolved decisions; questions remain visible.
func (l Ledger) Reviewed(m mutator.Mutator) bool {
	p, ok := m.(mutator.Identified)
	if !ok {
		return false
	}
	for _, e := range l.Entries {
		if e.Identity.ID == p.Identity().ID && e.Resolution == matched && e.Disposition != "needs-clarification" {
			return true
		}
	}

	return false
}

// encoding/json otherwise accepts duplicate keys with last-writer-wins semantics,
// which is unsuitable for a human review ledger.
func uniqueJSONKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read triage: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || !canonicalKeyPattern.MatchString(name) || seen[name] {
				return fmt.Errorf("duplicate or invalid triage key %q", key)
			}
			seen[name] = true
			if err := uniqueJSONKeys(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := uniqueJSONKeys(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid triage JSON delimiter")
	}
	_, err = decoder.Token()

	return err
}
