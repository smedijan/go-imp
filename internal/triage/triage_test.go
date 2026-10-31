package triage_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/triage"
)

type identified struct {
	mutator.Mutator
	identity mutator.Identity
}

func (m identified) Identity() mutator.Identity { return m.identity }

func ledger() triage.Ledger {
	return triage.Ledger{Version: 1, Module: "example.test", Entries: []triage.Entry{{Identity: mutator.Identity{ID: "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), Version: 1, ModulePath: "source.go", Original: "+", Replacement: "-"}, Disposition: "accepted-gap", Reason: "boundary assertion deferred", Provenance: "Kaj; issue #1; 2026-05-01"}}}
}

func TestReadStrictLedger(t *testing.T) {
	valid, err := json.Marshal(ledger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := triage.Read(strings.NewReader(string(valid)), "example.test"); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		string(valid) + " {}", strings.Replace(string(valid), `"version":1`, `"version":2`, 1),
		strings.Replace(string(valid), `"reason":"boundary assertion deferred"`, `"reason":" "`, 1),
		strings.Replace(string(valid), `"disposition":"accepted-gap"`, `"disposition":"equivalent"`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":1,"surprise":true`, 1),
		strings.Replace(string(valid), `"version":1`, `"version":2,"version":1`, 1),
		`{"version":1,"module":"example.test"}`, `null`,
	}
	for _, input := range cases {
		if _, err := triage.Read(strings.NewReader(input), "example.test"); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	if _, err := triage.Read(strings.NewReader(string(valid)), "other"); err == nil {
		t.Fatal("accepted other module")
	}
	duplicate := ledger()
	duplicate.Entries = append(duplicate.Entries, duplicate.Entries[0])
	if err := duplicate.Validate(duplicate.Module); err == nil {
		t.Fatal("accepted duplicate anchor")
	}
}

func TestResolveExactStaleUnmatchedAmbiguous(t *testing.T) {
	input := ledger()
	original := input.Entries[0].Identity
	changedSnapshot := original
	changedSnapshot.Original = "*"
	for _, tc := range []struct {
		name       string
		mutants    []mutator.Mutator
		resolution string
	}{
		{"exact", []mutator.Mutator{identified{identity: original}}, "matched"},
		{"scope omitted or disabled or changed", nil, "unmatched"},
		{"stale snapshot", []mutator.Mutator{identified{identity: changedSnapshot}}, "stale"},
		{"collision", []mutator.Mutator{identified{identity: original}, identified{identity: original}}, "ambiguous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved := triage.Resolve(input, tc.mutants)
			if resolved.Entries[0].Resolution != tc.resolution {
				t.Fatalf("%+v", resolved)
			}
			if input.Entries[0].Resolution != "" {
				t.Fatal("mutated input")
			}
			if resolved.Reviewed(identified{identity: original}) != (tc.resolution == "matched") {
				t.Fatal("unsafe review classification")
			}
		})
	}
	input.Entries[0].Disposition = "needs-clarification"
	if triage.Resolve(input, []mutator.Mutator{identified{identity: original}}).Reviewed(identified{identity: original}) {
		t.Fatal("question was hidden")
	}
}

func TestReadRejectsNoncanonicalKeySpellings(t *testing.T) {
	valid, err := json.Marshal(ledger())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, old, replacement string }{
		{"conflicting ledger version", `"version":1`, `"version":2,"Version":1`},
		{"conflicting disposition", `"disposition":"accepted-gap"`, `"disposition":"needs-clarification","Disposition":"accepted-gap"`},
		{"nested identity version", `"identity":{`, `"identity":{"Version":2,`},
		{"nested identity ID", `"id":`, `"ID":`},
		{"module case alias", `"module":`, `"Module":`},
		{"entries case alias", `"entries":`, `"Entries":`},
		{"unicode case alias", `"reason":`, `"reaſon":`},
		{"nested module path case alias", `"module_path":`, `"MODULE_PATH":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(string(valid), tc.old, tc.replacement, 1)
			if input == string(valid) {
				t.Fatal("test did not change input")
			}
			if _, err := triage.Read(strings.NewReader(input), "example.test"); err == nil {
				t.Fatalf("accepted noncanonical keys: %s", input)
			}
		})
	}
}
