package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/ledger"
	"github.com/smedijan/go-imp/internal/report"
)

func TestReportLedgerHistoricalEvidenceDoesNotCountAsCurrent(t *testing.T) {
	results := report.Results{Evidence: []ledger.Evidence{{ID: "old-id", Provenance: "stale", Confidence: "historical", Scope: "whole-module/dependency/environment", Previous: "KILLED", Transition: "removed", Confirmations: 2}}, LedgerNote: "historical outcomes only"}
	var output bytes.Buffer
	if err := report.Write(&output, results, report.Options{JSON: true, EfficacyThreshold: 99}); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Total    int               `json:"mutants_total"`
		Killed   int               `json:"mutants_killed"`
		Evidence []ledger.Evidence `json:"evidence"`
		Note     string            `json:"ledger_note"`
	}
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Total != 0 || document.Killed != 0 || len(document.Evidence) != 1 || document.Evidence[0].Provenance != "stale" || document.Note != results.LedgerNote {
		t.Fatal(document)
	}
	output.Reset()
	if err := report.Write(&output, results, report.Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Ledger: historical outcomes only\n") || !strings.Contains(output.String(), "stale (historical,") || !strings.Contains(output.String(), "historical=KILLED") {
		t.Fatal(output.String())
	}
}

func TestReportLedgerEmptyNoteIsOmitted(t *testing.T) {
	var output bytes.Buffer
	if err := report.Write(&output, report.Results{}, report.Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Ledger:") {
		t.Fatal("empty ledger note rendered", output.String())
	}
}
