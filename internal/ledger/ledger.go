// Package ledger stores bounded historical mutation evidence. It never schedules tests.
package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/smedijan/go-imp/internal/mutator"
)

const maxBytes = 4 << 20

// Options controls opt-in storage. The path must be outside all fingerprint roots.
type Options struct {
	Path        string
	Refresh     bool
	TTL         time.Duration
	MaxEntries  int
	Fingerprint func(context.Context) (string, error)
}

type entry struct {
	ID            string         `json:"id"`
	Fingerprint   string         `json:"fingerprint"`
	Status        mutator.Status `json:"status"`
	Confirmations int            `json:"confirmations"`
	Flaky         bool           `json:"flaky"`
	Observed      time.Time      `json:"observed"`
}
type disk struct {
	Version      int              `json:"version"`
	Inconclusive bool             `json:"inconclusive"`
	Entries      map[string]entry `json:"entries"`
}

// Evidence keeps historical outcomes separate from the current run's status.
type Evidence struct {
	ID            string `json:"id"`
	Provenance    string `json:"provenance"`
	Confidence    string `json:"confidence"`
	Scope         string `json:"scope"`
	Previous      string `json:"previous,omitempty"`
	Transition    string `json:"transition,omitempty"`
	Confirmations int    `json:"confirmations"`
}

// Session owns a non-blocking exclusive lock until Close. A crashed lock is not
// stolen: subsequent runs bypass storage until the owner removes the lock.
type Session struct {
	options      Options
	fingerprint  string
	entries      map[string]entry
	reused       map[string]bool
	locked       bool
	Reason       string
	Unstable     bool
	inconclusive bool
}

// Open fails closed to fresh execution, including on corruption or lock conflict.
func Open(ctx context.Context, options Options) *Session {
	s := &Session{options: options, entries: make(map[string]entry), reused: make(map[string]bool)}
	if options.TTL <= 0 || options.TTL > 30*24*time.Hour || options.MaxEntries < 1 || options.MaxEntries > 10000 {
		s.Reason = "invalid retention (TTL 1h..720h, entries 1..10000)"

		return s
	}
	if err := os.MkdirAll(filepath.Dir(options.Path), 0700); err != nil {
		s.Reason = "storage unavailable"

		return s
	}
	lock, err := os.OpenFile(options.Path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		s.Reason = "storage locked or unavailable"

		return s
	}
	_ = lock.Close()
	s.locked = true
	s.fingerprint, err = options.Fingerprint(ctx)
	if err != nil {
		s.Reason = "fingerprint unavailable: " + err.Error()

		return s
	}
	if options.Refresh {
		return s
	}
	data, err := readBounded(options.Path)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	var stored disk
	if err != nil || len(data) > maxBytes || json.Unmarshal(data, &stored) != nil || stored.Version != 1 || len(stored.Entries) > 10000 {
		s.Reason = "corrupt or incompatible ledger; refresh required"

		return s
	}
	for id, e := range stored.Entries {
		if id != e.ID || e.Confirmations < 0 || e.Confirmations > 2 || e.Status < mutator.NotCovered || e.Status > mutator.TimedOut || e.Fingerprint == "" || e.Observed.After(time.Now()) {
			s.Reason = "invalid ledger evidence; refresh required"

			return s
		}
	}
	s.inconclusive = stored.Inconclusive
	for _, e := range stored.Entries {
		if e.Flaky {
			s.inconclusive = true
		}
	}
	for id, e := range stored.Entries {
		if time.Since(e.Observed) < options.TTL {
			s.entries[id] = e
		}
	}

	return s
}

// Close releases only this session's lock; it never removes another owner's lock.
func (s *Session) Close() {
	if s.locked {
		_ = os.Remove(s.options.Path + ".lock")
		s.locked = false
	}
}

func conclusive(status mutator.Status) bool {
	return status == mutator.Killed || status == mutator.Lived
}

// Reuse requires two independent completed fresh runs with no observed disagreement.
func (s *Session) Reuse(id string) (mutator.Status, bool) {
	e, ok := s.entries[id]
	ok = ok && !s.inconclusive && s.Reason == "" && e.Fingerprint == s.fingerprint && e.Confirmations >= 2 && time.Since(e.Observed) < s.options.TTL && !e.Flaky && conclusive(e.Status)
	if ok {
		s.reused[id] = true
	}

	return e.Status, ok
}

// Finish reports transitions and publishes only a completely successful,
// fingerprint-stable run. Observed disagreement poisons agreement until refresh.
func (s *Session) Finish(ctx context.Context, current map[string]mutator.Status, missing func(string) string, complete bool) []Evidence {
	evidence := make([]Evidence, 0, len(current))
	now := time.Now()
	next := make(map[string]entry)
	for id, old := range s.entries {
		next[id] = old
	}
	for id, status := range current {
		e, n := s.observation(id, status, now)
		if n.Flaky {
			s.inconclusive = true
		}
		evidence = append(evidence, e)
		next[id] = n
	}
	for id, old := range s.entries {
		if _, ok := current[id]; !ok {
			evidence = append(evidence, Evidence{ID: id, Provenance: "stale", Confidence: "historical", Scope: "whole-module/dependency/environment", Previous: old.Status.String(), Transition: missing(id), Confirmations: old.Confirmations})
		}
	}
	if s.inconclusive {
		for i := range evidence {
			if evidence[i].Provenance != "stale" {
				evidence[i].Confidence = "inconclusive"
			}
		}
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
	if !complete || ctx.Err() != nil || s.Reason != "" || !s.locked {
		return evidence
	}
	fingerprint, err := s.options.Fingerprint(ctx)
	if err != nil || fingerprint != s.fingerprint || ctx.Err() != nil {
		s.Reason = "inputs changed during run; evidence not published"
		s.Unstable = true

		return evidence
	}
	ids := make([]string, 0, len(next))
	for id := range next {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := next[ids[i]], next[ids[j]]
		if a.Observed.Equal(b.Observed) {
			return ids[i] < ids[j]
		}

		return a.Observed.After(b.Observed)
	})
	bounded := make(map[string]entry)
	for _, id := range ids {
		if len(bounded) >= s.options.MaxEntries {
			break
		}
		if now.Sub(next[id].Observed) < s.options.TTL {
			bounded[id] = next[id]
		}
	}
	data, err := json.Marshal(disk{Version: 1, Inconclusive: s.inconclusive, Entries: bounded})
	if err != nil || len(data) > maxBytes {
		s.Reason = "ledger byte limit reached"

		return evidence
	}
	if err = atomicWrite(ctx, s.options.Path, data); err != nil {
		s.Reason = "ledger publication failed"
	}

	return evidence
}

func atomicWrite(ctx context.Context, path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".imp-ledger-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()

		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()

		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("publish ledger: %w", err)
	}

	return nil
}

// Validate rechecks inputs after baseline and discovery, before any reuse.
func (s *Session) Validate(ctx context.Context) {
	if s.Reason != "" {
		return
	}
	fingerprint, err := s.options.Fingerprint(ctx)
	if err != nil || fingerprint != s.fingerprint {
		s.Reason = "inputs changed during baseline/discovery"
		s.Unstable = true
	}
}

func readBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("nonregular or oversized ledger")
	}

	// #nosec G304 -- fixed per-module user-cache ledger path, bounded read.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	return io.ReadAll(io.LimitReader(f, maxBytes+1))
}

func (s *Session) observation(id string, status mutator.Status, now time.Time) (Evidence, entry) {
	old, exists := s.entries[id]
	e := Evidence{ID: id, Provenance: "fresh", Confidence: "unconfirmed", Scope: "whole-module/dependency/environment"}
	if exists {
		e.Previous = old.Status.String()
		e.Transition = "unresolved"
	}
	if s.reused[id] {
		e.Provenance = "reused"
		e.Confidence = "confirmed"
	}
	if exists && conclusive(status) && conclusive(old.Status) {
		if old.Status != status {
			if old.Status == mutator.Lived {
				e.Transition = "lived-to-killed"
			} else {
				e.Transition = "killed-to-lived"
			}
		} else {
			e.Transition = "unchanged"
		}
	}
	n := entry{ID: id, Fingerprint: s.fingerprint, Status: status, Observed: now}
	if old.Flaky {
		n.Flaky = true
	}
	if conclusive(status) {
		n.Confirmations = 1
	}
	if exists && old.Fingerprint == s.fingerprint {
		n.Flaky = n.Flaky || old.Status != status
		if old.Status == status && conclusive(status) {
			n.Confirmations = min(2, old.Confirmations+1)
		}
	}
	if s.reused[id] {
		n = old
	}
	e.Confirmations = n.Confirmations
	if n.Flaky {
		e.Confidence = "inconclusive"
	} else if n.Confirmations >= 2 {
		e.Confidence = "confirmed"
	}

	return e, n
}
