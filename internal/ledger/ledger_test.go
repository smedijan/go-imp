package ledger_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/ledger"
	"github.com/smedijan/go-imp/internal/mutator"
)

func options(t testing.TB) ledger.Options {
	t.Helper()

	return ledger.Options{Path: filepath.Join(t.TempDir(), "outcomes.json"), TTL: time.Hour, MaxEntries: 20, Fingerprint: func(context.Context) (string, error) { return "fingerprint", nil }}
}
func finish(t testing.TB, o ledger.Options, status mutator.Status) []ledger.Evidence {
	t.Helper()
	s := ledger.Open(context.Background(), o)
	defer s.Close()
	if s.Reason != "" {
		t.Fatal(s.Reason)
	}

	evidence := s.Finish(context.Background(), map[string]mutator.Status{"id": status}, func(string) string { return "removed" }, true)
	if s.Reason != "" {
		t.Fatal(s.Reason)
	}

	return evidence
}
func TestLedgerIndependentConfirmationsAndInvalidation(t *testing.T) {
	o := options(t)
	for i := 0; i < 2; i++ {
		s := ledger.Open(context.Background(), o)
		if _, ok := s.Reuse("id"); ok {
			t.Fatal("premature reuse")
		}
		evidence := s.Finish(context.Background(), map[string]mutator.Status{"id": mutator.Killed}, func(string) string { return "removed" }, true)
		confidence := "unconfirmed"
		if i == 1 {
			confidence = "confirmed"
		}
		if len(evidence) != 1 || evidence[0].Confidence != confidence || evidence[0].Confirmations != i+1 || evidence[0].Provenance != "fresh" {
			t.Fatal(evidence)
		}
		if s.Reason != "" {
			t.Fatal(s.Reason)
		}
		s.Close()
	}
	s := ledger.Open(context.Background(), o)
	if status, ok := s.Reuse("id"); !ok || status != mutator.Killed {
		t.Fatal("missing confirmed reuse")
	}
	s.Close()
	o.Fingerprint = func(context.Context) (string, error) { return "test-only change", nil }
	s = ledger.Open(context.Background(), o)
	defer s.Close()
	if _, ok := s.Reuse("id"); ok {
		t.Fatal("reused stale kill")
	}
	e := s.Finish(context.Background(), map[string]mutator.Status{"id": mutator.Lived}, func(string) string { return "removed" }, true)
	if e[0].Transition != "killed-to-lived" || e[0].Provenance != "fresh" || e[0].Confirmations != 1 {
		t.Fatal(e)
	}
}
func TestLedgerDisagreementStickyUntilRefresh(t *testing.T) {
	for _, status := range []mutator.Status{mutator.NotCovered, mutator.Lived, mutator.Killed, mutator.TimedOut, mutator.NotViable, mutator.Skipped, mutator.Runnable} {
		t.Run(status.String(), func(t *testing.T) {
			o := options(t)
			finish(t, o, mutator.Killed)
			finish(t, o, status)
			finish(t, o, mutator.Killed)
			finish(t, o, mutator.Killed)
			s := ledger.Open(context.Background(), o)
			if _, ok := s.Reuse("id"); ok && status != mutator.Killed {
				t.Fatal("reused disagreement")
			}
			s.Close()
			o.Refresh = true
			finish(t, o, mutator.Killed)
			o.Refresh = false
			finish(t, o, mutator.Killed)
			s = ledger.Open(context.Background(), o)
			defer s.Close()
			if _, ok := s.Reuse("id"); !ok {
				t.Fatal("refresh failed")
			}
		})
	}
}
func TestLedgerTransitions(t *testing.T) {
	o := options(t)
	finish(t, o, mutator.Lived)
	o.Fingerprint = func(context.Context) (string, error) { return "changed tests", nil }
	if e := finish(t, o, mutator.Killed); e[0].Transition != "lived-to-killed" {
		t.Fatal(e)
	}
	for _, transition := range []string{"changed", "removed", "unresolved"} {
		s := ledger.Open(context.Background(), o)
		e := s.Finish(context.Background(), nil, func(string) string { return transition }, false)
		s.Close()
		if len(e) != 1 || e[0].Provenance != "stale" || e[0].Confidence != "historical" || e[0].Transition != transition {
			t.Fatal(e)
		}
	}
}
func TestLedgerCancellationAndConcurrentSourceEdit(t *testing.T) {
	o := options(t)
	finish(t, o, mutator.Killed)
	before, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"cancel", "incomplete", "source-edit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fingerprint := "fingerprint"
			o.Fingerprint = func(context.Context) (string, error) { return fingerprint, nil }
			s := ledger.Open(ctx, o)
			defer s.Close()
			if mode == "cancel" {
				cancel()
			}
			if mode == "source-edit" {
				fingerprint = "edited"
			}
			s.Finish(ctx, map[string]mutator.Status{"id": mutator.Killed}, func(string) string { return "removed" }, mode != "incomplete")
			after, err := os.ReadFile(o.Path)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("published interrupted or unstable evidence")
			}
		})
	}
}
func TestLedgerConcurrentLockNonDestructive(t *testing.T) {
	o := options(t)
	finish(t, o, mutator.Killed)
	owner := ledger.Open(context.Background(), o)
	defer owner.Close()
	before, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			s := ledger.Open(context.Background(), o)
			defer s.Close()
			if s.Reason == "" {
				t.Error("lock conflict not detected")
			}
			s.Finish(context.Background(), map[string]mutator.Status{"id": mutator.Lived}, func(string) string { return "removed" }, true)
		})
	}
	wg.Wait()
	after, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("lock contenders overwrote evidence")
	}
	if _, err := os.Stat(o.Path + ".lock"); err != nil {
		t.Fatal("contender removed owner lock")
	}
}
func TestLedgerCorruptionAndRetention(t *testing.T) {
	for _, data := range []string{"{", "{\"version\":999}", strings.Repeat("x", (4<<20)+1)} {
		t.Run("corruption", func(t *testing.T) {
			o := options(t)
			if err := os.WriteFile(o.Path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			s := ledger.Open(context.Background(), o)
			if s.Reason == "" {
				t.Fatal("corruption accepted")
			}
			s.Close()
			o.Refresh = true
			finish(t, o, mutator.Lived)
		})
	}
	o := options(t)
	o.MaxEntries = 1
	s := ledger.Open(context.Background(), o)
	s.Finish(context.Background(), map[string]mutator.Status{"a": mutator.Lived, "b": mutator.Killed}, func(string) string { return "removed" }, true)
	s.Close()
	data, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct{ Entries map[string]json.RawMessage }
	if err = json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Entries) != 1 || stored.Entries["a"] == nil {
		t.Fatal("entry cap or deterministic equal-time retention ignored", stored.Entries)
	}
	// Advance the recorded timestamp rather than sleeping in race tests.
	var raw map[string]any
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	entries, ok := raw["entries"].(map[string]any)
	if !ok {
		t.Fatal(raw)
	}
	for _, value := range entries {
		e, ok := value.(map[string]any)
		if !ok {
			t.Fatal(value)
		}
		e["observed"] = time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	}
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s = ledger.Open(context.Background(), o)
	defer s.Close()
	if e := s.Finish(context.Background(), nil, func(string) string { return "removed" }, true); len(e) != 0 {
		t.Fatal("expired evidence retained", e)
	}
}
func BenchmarkLedgerRepeatedWorkflow(b *testing.B) {
	o := options(b)
	for i := 0; i < b.N; i++ {
		s := ledger.Open(context.Background(), o)
		s.Reuse("id")
		s.Finish(context.Background(), map[string]mutator.Status{"id": mutator.Killed}, func(string) string { return "removed" }, true)
		s.Close()
	}
}

func TestLedgerFlakyInhibitionSurvivesRetention(t *testing.T) {
	o := options(t)
	o.MaxEntries = 1
	finish(t, o, mutator.Lived)
	finish(t, o, mutator.Killed)
	s := ledger.Open(context.Background(), o)
	// Evict the flaky finding with a different, repeatedly agreeing finding.
	s.Finish(context.Background(), map[string]mutator.Status{"replacement": mutator.Killed}, func(string) string { return "removed" }, true)
	s.Close()
	for i := 0; i < 3; i++ {
		s = ledger.Open(context.Background(), o)
		if _, ok := s.Reuse("replacement"); ok {
			t.Fatal("eviction erased sticky disagreement")
		}
		s.Finish(context.Background(), map[string]mutator.Status{"replacement": mutator.Killed}, func(string) string { return "removed" }, true)
		s.Close()
	}
}

func TestLedgerValidationBeforeReuse(t *testing.T) {
	o := options(t)
	finish(t, o, mutator.Killed)
	finish(t, o, mutator.Killed)
	fingerprint := "fingerprint"
	o.Fingerprint = func(context.Context) (string, error) { return fingerprint, nil }
	s := ledger.Open(context.Background(), o)
	defer s.Close()
	s.Validate(context.Background())
	if s.Reason != "" || s.Unstable {
		t.Fatal("unchanged fingerprint rejected", s.Reason)
	}
	fingerprint = "concurrent source edit"
	s.Validate(context.Background())
	if _, ok := s.Reuse("id"); ok || !s.Unstable {
		t.Fatal("reused inputs modified during baseline")
	}
}

func TestLedgerRejectsNonregularStorageAndReuseDoesNotExtendTTL(t *testing.T) {
	o := options(t)
	if err := os.Mkdir(o.Path, 0700); err != nil {
		t.Fatal(err)
	}
	s := ledger.Open(context.Background(), o)
	if s.Reason == "" {
		t.Fatal("directory accepted as ledger")
	}
	s.Close()
	if err := os.Remove(o.Path); err != nil {
		t.Fatal(err)
	}
	finish(t, o, mutator.Killed)
	finish(t, o, mutator.Killed)
	before, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	s = ledger.Open(context.Background(), o)
	if _, ok := s.Reuse("id"); !ok {
		t.Fatal("missing confirmed outcome")
	}
	evidence := s.Finish(context.Background(), map[string]mutator.Status{"id": mutator.Killed}, func(string) string { return "removed" }, true)
	s.Close()
	after, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || evidence[0].Provenance != "reused" {
		t.Fatal("reuse extended actual observation time")
	}
}

func TestLedgerRetentionOptionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		ttl     time.Duration
		entries int
		valid   bool
	}{
		{0, 1, false}, {time.Hour, 0, false}, {time.Hour, 10001, false}, {721 * time.Hour, 1, false},
		{time.Hour, 1, true}, {720 * time.Hour, 10000, true},
	} {
		o := options(t)
		o.TTL = tc.ttl
		o.MaxEntries = tc.entries
		s := ledger.Open(context.Background(), o)
		s.Close()
		if (s.Reason == "") != tc.valid {
			t.Fatalf("TTL=%s entries=%d valid=%t: %s", tc.ttl, tc.entries, tc.valid, s.Reason)
		}
	}
}

func TestLedgerStickyConfidencePreservesHistoricalEvidence(t *testing.T) {
	o := options(t)
	s := ledger.Open(context.Background(), o)
	s.Finish(context.Background(), map[string]mutator.Status{"current": mutator.Killed, "removed": mutator.Lived}, func(string) string { return "removed" }, true)
	s.Close()
	s = ledger.Open(context.Background(), o)
	evidence := s.Finish(context.Background(), map[string]mutator.Status{"current": mutator.Lived}, func(string) string { return "removed" }, true)
	s.Close()
	if len(evidence) != 2 || evidence[0].ID != "current" || evidence[0].Confidence != "inconclusive" || evidence[1].ID != "removed" || evidence[1].Confidence != "historical" || evidence[1].Provenance != "stale" {
		t.Fatal(evidence)
	}
}

func TestLedgerMaximumStorageBoundaries(t *testing.T) {
	o := options(t)
	// A valid document exactly at the byte limit must remain readable. Padding
	// precedes the final brace so a truncated read is observably invalid JSON.
	now := time.Now().Add(-time.Minute)
	entry := map[string]any{"id": "id", "fingerprint": "fingerprint", "status": mutator.Killed, "confirmations": 2, "observed": now}
	data, err := json.Marshal(map[string]any{"version": 1, "entries": map[string]any{"id": entry}})
	if err != nil {
		t.Fatal(err)
	}
	padded := append([]byte(nil), data[:len(data)-1]...)
	padded = append(padded, []byte(strings.Repeat(" ", (4<<20)-len(data)))...)
	padded = append(padded, '}')
	if err = os.WriteFile(o.Path, padded, 0600); err != nil {
		t.Fatal(err)
	}
	s := ledger.Open(context.Background(), o)
	if s.Reason != "" {
		t.Fatal("valid maximum-size ledger rejected", s.Reason)
	}
	if _, ok := s.Reuse("id"); !ok {
		t.Fatal("maximum-size evidence unavailable")
	}
	s.Close()
	// The independently bounded entry limit permits exactly 10000 historical
	// records even when this run's configured retention cap is smaller.
	entries := make(map[string]any, 10000)
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("id-%05d", i)
		entries[id] = map[string]any{"id": id, "fingerprint": "fingerprint", "status": mutator.NotCovered, "confirmations": 0, "observed": now}
	}
	data, err = json.Marshal(map[string]any{"version": 1, "entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s = ledger.Open(context.Background(), o)
	defer s.Close()
	if s.Reason != "" {
		t.Fatal("valid maximum entry count rejected", s.Reason)
	}
	evidence := s.Finish(context.Background(), nil, func(string) string { return "removed" }, false)
	if len(evidence) != 10000 || evidence[0].ID != "id-00000" || evidence[len(evidence)-1].ID != "id-09999" {
		t.Fatal("historical evidence is incomplete or unordered")
	}
}

func TestLedgerPublicationFailureIsReportedWithoutReplacingDirectory(t *testing.T) {
	o := options(t)
	o.Refresh = true
	if err := os.Mkdir(o.Path, 0700); err != nil {
		t.Fatal(err)
	}
	s := ledger.Open(context.Background(), o)
	defer s.Close()
	if s.Reason != "" {
		t.Fatal(s.Reason)
	}
	s.Finish(context.Background(), map[string]mutator.Status{"id": mutator.Killed}, func(string) string { return "removed" }, true)
	if s.Reason != "ledger publication failed" {
		t.Fatal("publication failure not reported", s.Reason)
	}
	info, err := os.Stat(o.Path)
	if err != nil || !info.IsDir() {
		t.Fatal("failed publication replaced destination", err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(o.Path), ".imp-ledger-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("partial publication artifacts remain", files, err)
	}
}

func TestLedgerReuseKilledDoesNotPromoteSurvivor(t *testing.T) {
	o := options(t)
	finish(t, o, mutator.Lived)
	finish(t, o, mutator.Lived)
	s := ledger.Open(context.Background(), o)
	defer s.Close()
	if _, ok := s.ReuseKilled("id"); ok {
		t.Fatal("detection-only policy reused survivor")
	}
	evidence := s.Finish(context.Background(), map[string]mutator.Status{"id": mutator.Runnable}, func(string) string { return "removed" }, true)
	if evidence[0].Provenance != "fresh" || evidence[0].Confidence != "inconclusive" {
		t.Fatal("declined reuse marked as reused or retained survivor", evidence)
	}
}
