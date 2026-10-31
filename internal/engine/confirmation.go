package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/smedijan/go-imp/internal/engine/workdir"
	"github.com/smedijan/go-imp/internal/mutator"
)

const repeatMutantRole = "repeat-mutant"

// confirm never repeats a whole campaign: only a suspicious result gets a
// baseline and, if that passes, one mutant repeat. Both share a hard time budget.
func (m *mutantExecutor) confirm(parent context.Context, original mutator.Status, evidence *mutator.ExecutionEvidence, cancel context.CancelFunc) (mutator.Status, error) {
	ctx, stop := context.WithTimeout(parent, 2*m.executor.testExecutionTime)
	defer stop()
	initial := *evidence
	initial.Role = "initial-mutant"
	evidence.Attempts = []mutator.ExecutionEvidence{initial}
	evidence.Confidence = "unresolved-timeout"
	unknown := mutator.NotViable
	if original == mutator.TimedOut {
		unknown = mutator.TimedOut
	}
	dealer := workdir.NewCachedDealer(m.executor.wdDealer.WorkDir(), m.executor.pristine, ".git")
	defer dealer.Clean()
	oldWorkdir := m.mutant.Workdir()
	defer m.mutant.SetWorkdir(oldWorkdir)
	for _, role := range []string{"baseline", repeatMutantRole} {
		if ctx.Err() != nil {
			return unknown, nil
		}
		root, err := dealer.GetContext(ctx, role)
		if err != nil {
			if ctx.Err() != nil {
				return unknown, nil
			}

			return unknown, fmt.Errorf("prepare confirmation: %w", err)
		}
		if ctx.Err() != nil {
			return unknown, nil
		}
		m.mutant.SetWorkdir(filepath.Join(root, m.executor.mod.CallingDir))
		if role == repeatMutantRole {
			if err := m.mutant.Apply(); err != nil {
				cancel()

				return unknown, errors.Join(fmt.Errorf("apply confirmation mutation: %w", err), m.rollback(cancel))
			}
		}
		status, attempt := m.runTests(ctx, root, m.mutant.Pkg())
		attempt.Role = role
		evidence.Attempts = append(evidence.Attempts, *attempt)
		if role == repeatMutantRole {
			if err := m.rollback(cancel); err != nil {
				return unknown, err
			}
		}
		if parent.Err() != nil {
			return unknown, errExecutionStopped
		}
		if ctx.Err() != nil || status == mutator.TimedOut {
			evidence.Confidence = "unresolved-timeout"

			return unknown, nil
		}
		if status == mutator.NotViable || status == mutator.Runnable {
			evidence.Confidence = "infrastructure"

			return unknown, nil
		}
		if role == "baseline" {
			if status != mutator.Lived {
				evidence.Confidence = "baseline-unstable"

				return unknown, nil
			}

			continue
		}
		if original == mutator.Killed && status == mutator.Killed {
			evidence.Confidence = "confirmed"

			return mutator.Killed, nil
		}
		evidence.Confidence = "inconsistent"
	}

	return unknown, nil
}
