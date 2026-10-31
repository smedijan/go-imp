package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/smedijan/go-imp/internal/ledger"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

// WithLedger attaches opt-in evidence reuse without changing executor scheduling.
func WithLedger(session *ledger.Session) Option {
	return func(m Engine) Engine {
		m.ledger = session

		return m
	}
}

func (mu *Engine) executeWithLedger(ctx context.Context, discovered []mutator.Mutator, replay []mutator.ReplayTarget, execute func([]mutator.Mutator) (report.Results, error)) (report.Results, error) {
	if mu.ledger == nil {
		return execute(discovered)
	}
	mu.ledger.Validate(ctx)
	if mu.ledger.Unstable {
		return report.Results{}, fmt.Errorf("incremental inputs changed during baseline/discovery")
	}
	var pending, reused []mutator.Mutator
	counts := make(map[string]int)
	for _, m := range discovered {
		if identified, ok := m.(mutator.Identified); ok {
			counts[identified.Identity().ID]++
		}
	}
	for _, m := range discovered {
		id := ""
		if identified, ok := m.(mutator.Identified); ok {
			id = identified.Identity().ID
		}
		if id != "" && counts[id] == 1 && m.Status() == mutator.Runnable {
			if status, ok := mu.ledger.Reuse(id); ok {
				m.SetStatus(status)
				reused = append(reused, m)

				continue
			}
		}
		pending = append(pending, m)
	}
	results, err := execute(pending)
	results.Mutants = append(results.Mutants, reused...)
	current := make(map[string]mutator.Status)
	for _, m := range results.Mutants {
		if identified, ok := m.(mutator.Identified); ok && identified.Identity().ID != "" && counts[identified.Identity().ID] == 1 {
			current[identified.Identity().ID] = m.Status()
		}
	}
	missing := func(id string) string {
		if len(mu.codeData.TargetIDs) > 0 || counts[id] > 1 {
			return "unresolved"
		}
		parts := strings.Split(id, ":")
		if len(parts) == 3 && mu.sourceLocators[parts[1]] {
			return "changed"
		}

		return "removed"
	}
	complete := err == nil
	for _, target := range replay {
		if target.Resolution != replayMatched {
			complete = false
		}
	}
	results.Evidence = mu.ledger.Finish(ctx, current, missing, complete)
	results.LedgerNote = mu.ledger.Reason
	if mu.ledger.Unstable {
		return results, fmt.Errorf("incremental inputs changed during execution")
	}

	return results, err
}
