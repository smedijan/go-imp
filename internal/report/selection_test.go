package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/report"
)

func TestReportSelectionExplanation(t *testing.T) {
	results := report.Results{Selection: &diff.Selection{Requested: diff.ChangedLine, Effective: diff.AffectedPackage, Reasons: []string{"test change: pkg/value_test.go"}}}
	for _, jsonOutput := range []bool{false, true} {
		var out bytes.Buffer
		if err := report.Write(&out, results, report.Options{JSON: jsonOutput}); err != nil {
			t.Fatal(err)
		}
		if jsonOutput {
			var document struct {
				Selection diff.Selection `json:"selection"`
			}
			if err := json.Unmarshal(out.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			if document.Selection.Requested != diff.ChangedLine || document.Selection.Effective != diff.AffectedPackage || len(document.Selection.Reasons) != 1 {
				t.Fatalf("missing selection: %s", &out)
			}
		} else if !strings.Contains(out.String(), "requested=changed-line effective=affected-package") || !strings.Contains(out.String(), "test change: pkg/value_test.go") {
			t.Fatalf("missing selection: %s", &out)
		}
	}
}
