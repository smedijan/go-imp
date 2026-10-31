package engine

import (
	"go/ast"
	"go/token"
	"unicode/utf8"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/mutator"
)

const observationNameLimit = 1024

func observedName(value string) mutator.ObservedName {
	name := mutator.ObservedName{Known: value != "", Value: value}
	if len(value) > observationNameLimit {
		end := observationNameLimit
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		name.Value, name.Truncated = value[:end], true
	}

	return name
}

// Reuse parsed discovery declarations, including the top-level owner of closures.
// Anonymous functions are not assigned speculative names or new identities.
func enclosingDeclaration(set *token.FileSet, file *ast.File, offset int) ast.Decl {
	for _, decl := range file.Decls {
		if offset >= set.PositionFor(decl.Pos(), false).Offset && offset < set.PositionFor(decl.End(), false).Offset {
			return decl
		}
	}

	return nil
}

func (mu *Engine) observeMutation(pkg string, set *token.FileSet, file *ast.File, pos token.Position) (mutator.Status, *mutator.ObservationContext) {
	selection := mutator.SelectionObservation{Discovery: "active-build", File: "included", Coverage: "not-covered", Diff: "unrestricted", Replay: "not-requested", DryRun: configuration.Get[bool](configuration.RunDryRunKey), ImpactProvenance: "unknown"}
	if mu.customFS {
		selection.Discovery = "syntax-only"
	}
	status := mutator.NotCovered
	if mu.codeData.Cov.IsCovered(pos) {
		status = mutator.Runnable
		selection.Coverage = "covered"
	}
	if len(mu.codeData.Diff) > 0 {
		selection.Diff = "changed"
	}
	if !mu.codeData.Diff.IsChanged(pos) {
		status = mutator.Skipped
		selection.Diff = "outside-diff"
	}
	switch {
	case status == mutator.Skipped:
		selection.EligibilityReason = "outside-diff"
	case status == mutator.NotCovered:
		selection.EligibilityReason = "not-covered"
	case selection.DryRun:
		selection.EligibilityReason = "dry-run"
	default:
		selection.EligibilityReason = "covered-and-in-scope"
	}
	context := &mutator.ObservationContext{Package: observedName(file.Name.Name), Selection: selection}
	// Syntax-only fixtures do not establish a resolved Go import path.
	if !mu.customFS {
		context.PackagePath = observedName(pkg)
	}
	if decl := enclosingDeclaration(set, file, pos.Offset); decl != nil {
		context.EnclosingSymbol = observedName(declarationName(decl))
	}

	return status, context
}

func observeReplaySelection(mutants []mutator.Mutator, requested bool) {
	if !requested {
		return
	}
	for _, mutant := range mutants {
		if observer, ok := mutant.(mutator.ObservationProvider); ok {
			if context := observer.ObservationContext(); context != nil {
				context.Selection.Replay = replayMatched
			}
		}
	}
}

// The executor's captured mode is authoritative if configuration changed after
// construction. Discovery-only adapters retain the observed configuration mode.
func observeExecutionMode(mutant mutator.Mutator, dryRun bool) {
	observer, ok := mutant.(mutator.ObservationProvider)
	if !ok || observer.ObservationContext() == nil {
		return
	}
	context := observer.ObservationContext()
	context.Selection.DryRun = dryRun
	if mutant.Status() == mutator.Runnable {
		context.Selection.EligibilityReason = "covered-and-in-scope"
		if dryRun {
			context.Selection.EligibilityReason = "dry-run"
		}
	}
}
