package engine_test

import (
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/triage"
)

func TestTriageCanonicalLineShiftEditsAndNeighbors(t *testing.T) {
	enableIdentityOperators(t)
	source := "package calc\nfunc Sub(a,b int) int { x := a-b; return x-b }\n"
	first := discoverIdentity(t, source, engine.CodeData{}, ".")
	if len(first.Mutants) < 2 {
		t.Fatal("missing neighboring expressions")
	}
	identified, ok := first.Mutants[0].(mutator.Identified)
	if !ok {
		t.Fatal("missing identity")
	}
	anchor := identified.Identity()
	ledger := triage.Ledger{Version: 1, Module: first.Module, Entries: []triage.Entry{{Identity: anchor, Disposition: "intentional-behavior", Reason: "reviewed this edit only", Provenance: "reviewer"}}}
	shifted := discoverIdentity(t, "// header\n\n"+source, engine.CodeData{}, ".")
	resolved := triage.Resolve(ledger, shifted.Mutants)
	if resolved.Entries[0].Resolution != "matched" {
		t.Fatalf("line shift invalidated %+v", resolved)
	}
	reviewed := 0
	for _, m := range shifted.Mutants {
		if resolved.Reviewed(m) {
			reviewed++
		}
	}
	if reviewed != 1 {
		t.Fatalf("neighbor suppression: %d", reviewed)
	}
	changed := discoverIdentity(t, strings.Replace(source, "x := a-b", "x := a-b+1", 1), engine.CodeData{}, ".")
	resolved = triage.Resolve(ledger, changed.Mutants)
	if resolved.Entries[0].Resolution != "unmatched" {
		t.Fatal("relevant change reused disposition")
	}
	for _, m := range changed.Mutants {
		if resolved.Reviewed(m) {
			t.Fatal("changed source hidden")
		}
	}
}
