package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

func TestReportSelectionAndReplayExplanation(t *testing.T) {
	id := "imp-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	results := report.Results{Replay: []mutator.ReplayTarget{{ID: id, Resolution: "resolved"}}, Selection: &diff.Selection{Requested: diff.ChangedLine, Effective: diff.AffectedPackage, Reasons: []string{"test change: pkg/value_test.go"}}}
	for _, jsonOutput := range []bool{false, true} {
		var out bytes.Buffer
		if err := report.Write(&out, results, report.Options{JSON: jsonOutput}); err != nil {
			t.Fatal(err)
		}
		if jsonOutput {
			var document struct {
				Selection       diff.Selection         `json:"selection"`
				IdentityVersion int                    `json:"identity_version"`
				Replay          []mutator.ReplayTarget `json:"replay"`
			}
			if err := json.Unmarshal(out.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			if document.IdentityVersion != 1 || len(document.Replay) != 1 || document.Replay[0] != results.Replay[0] {
				t.Fatalf("missing replay/identity version: %s", &out)
			}
			if document.Selection.Requested != diff.ChangedLine || document.Selection.Effective != diff.AffectedPackage || len(document.Selection.Reasons) != 1 {
				t.Fatalf("missing selection: %s", &out)
			}
		} else if !strings.Contains(out.String(), "Replay "+id+": resolved") || !strings.Contains(out.String(), "requested=changed-line effective=affected-package") || !strings.Contains(out.String(), "test change: pkg/value_test.go") {
			t.Fatalf("missing selection: %s", &out)
		}
	}
}
