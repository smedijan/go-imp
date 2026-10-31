package report_test

import (
	"fmt"
	"strings"
	"testing"

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
		{"unsupported-schema", strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1), "example.test/replay"},
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
