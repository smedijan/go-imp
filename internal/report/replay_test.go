package report_test

import (
	"fmt"
	"go/token"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/mutator"

	"github.com/smedijan/go-imp/internal/report"
)

func TestReplayIDsValidatePriorReports(t *testing.T) {
	id := "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	valid := fmt.Sprintf(`{"schema_version":1,"identity_version":1,"go_module":"example.test/replay","groups":{"LIVED":{"calc.go":[{"id":%q,"identity":{"id":%q,"version":1}}]}}}`, id, id)
	ids, err := report.ReplayIDs(strings.NewReader(valid), "example.test/replay")
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("valid prior report: %v, %v", ids, err)
	}
	for _, tc := range []struct{ name, document, module string }{
		{"legacy", `{"schema_version":1,"go_module":"example.test/replay","groups":{"LIVED":{"calc.go":[{"line":2,"column":3,"type":"ARITHMETIC_BASE"}]}}}`, "example.test/replay"},
		{"unsupported-schema", strings.Replace(valid, `"schema_version":1`, `"schema_version":3`, 1), "example.test/replay"},
		{"zero-schema", strings.Replace(valid, `"schema_version":1`, `"schema_version":0`, 1), "example.test/replay"},
		{"unsupported-identity", strings.Replace(valid, `"identity_version":1`, `"identity_version":2`, 1), "example.test/replay"},
		{"unsupported-finding", strings.Replace(valid, `"version":1`, `"version":2`, 1), "example.test/replay"},
		{"inconsistent-id", strings.Replace(valid, id, "imp-v1:"+strings.Repeat("c", 64)+":"+strings.Repeat("d", 64), 1), "example.test/replay"},
		{"malformed-id", strings.ReplaceAll(valid, id, "line:2"), "example.test/replay"},
		{"wrong-module", valid, "other.example/module"},
		{"empty", `{"schema_version":1,"identity_version":1,"go_module":"example.test/replay","groups":{}}`, "example.test/replay"},
		{"truncated", valid[:len(valid)-1], "example.test/replay"},
		{"trailing-json", valid + "{}", "example.test/replay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := report.ReplayIDs(strings.NewReader(tc.document), tc.module); err == nil {
				t.Fatal("unsafe prior report accepted")
			}
		})
	}
}

func TestValidateMutantIDs(t *testing.T) {
	for _, id := range []string{"", "imp-v0:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), "imp-v1:xyz:abc", "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":extra"} {
		if err := report.ValidateMutantIDs([]string{id}); err == nil {
			t.Fatalf("accepted invalid ID %q", id)
		}
	}
}

type replayEvidenceMutant struct {
	evidenceMutant
	identity mutator.Identity
}

func (m *replayEvidenceMutant) Identity() mutator.Identity { return m.identity }
func (m *replayEvidenceMutant) Applied() bool              { return true }
func (m *replayEvidenceMutant) AppliedSourceHash() string  { return "observed-source-hash" }

func TestReplayIDsAcceptOwnEvidenceReport(t *testing.T) {
	id := "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	m := &replayEvidenceMutant{
		evidenceMutant: evidenceMutant{stubMutant: stubMutant{status: mutator.Killed, position: token.Position{Filename: "calc.go"}, original: []byte("a + b"), mutated: []byte("a - b")}},
		identity:       mutator.Identity{ID: id, Version: 1, ModulePath: "calc.go", Original: "+", Replacement: "-"},
	}
	m.SetExecutionEvidence(&mutator.ExecutionEvidence{Args: []string{"go", "test", "p"}, Category: "test"})
	document, payload := renderJSON(t, report.Results{Module: "example.test/replay", Mutants: []mutator.Mutator{m}}, report.Options{})
	finding := document.Groups["KILLED"]["calc.go"][0]
	if document.SchemaVersion != 2 || document.IdentityVersion != 1 || finding.ID != id || finding.Identity == nil || finding.Execution == nil || !finding.Applied || finding.AppliedSourceHash == "" || finding.OriginalSnippet != "a + b" || finding.MutatedSnippet != "a - b" {
		t.Fatalf("missing integrated identity/evidence: %+v", document)
	}
	for _, prior := range []string{payload, strings.Replace(payload, `"schema_version":2`, `"schema_version":1`, 1)} {
		ids, err := report.ReplayIDs(strings.NewReader(prior), "example.test/replay")
		if err != nil || len(ids) != 1 || ids[0] != id {
			t.Fatalf("own report cannot replay: ids=%v err=%v", ids, err)
		}
	}
}
