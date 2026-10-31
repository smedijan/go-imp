package mutator_test

import (
	"testing"

	"github.com/smedijan/go-imp/internal/mutator"
)

func TestExecutionEvidenceReusableDetection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence *mutator.ExecutionEvidence
		want     bool
	}{
		{name: "missing evidence"},
		{name: "empty evidence", evidence: &mutator.ExecutionEvidence{}},
		{name: "unverified test failure", evidence: &mutator.ExecutionEvidence{Category: "test"}},
		{name: "confirmed test failure", evidence: &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "test"}, want: true},
		{name: "unstable baseline", evidence: &mutator.ExecutionEvidence{Confidence: "baseline-unstable", Category: "test"}},
		{name: "inconsistent repeat", evidence: &mutator.ExecutionEvidence{Confidence: "inconsistent", Category: "test"}},
		{name: "unresolved timeout", evidence: &mutator.ExecutionEvidence{Confidence: "unresolved-timeout", Category: "timeout"}},
		{name: "infrastructure failure", evidence: &mutator.ExecutionEvidence{Confidence: "infrastructure", Category: "infrastructure"}},
		{name: "confirmed without category", evidence: &mutator.ExecutionEvidence{Confidence: "confirmed"}},
		{name: "confirmed success is not detection", evidence: &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "success"}},
		{name: "confirmed timeout is not detection", evidence: &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "timeout"}},
		{name: "confirmed infrastructure is not detection", evidence: &mutator.ExecutionEvidence{Confidence: "confirmed", Category: "infrastructure"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.evidence.ReusableDetection(); got != tc.want {
				t.Fatalf("ReusableDetection() = %t, want %t for %+v", got, tc.want, tc.evidence)
			}
		})
	}
}
