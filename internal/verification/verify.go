package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

const (
	outcomeInconclusive    = "inconclusive"
	outcomeFlaky           = "flaky"
	observedPass           = "pass"
	outcomeUnrelated       = "unrelated"
	observedInfrastructure = "infrastructure"
	outcomeTimeout         = "timeout"
)

// Options are explicit bounds, independent of normal run configuration.
type Options struct {
	Root, Baseline, Package, Test string
	IDs                           []string
	Timeout                       time.Duration
}

// Result retains every observation used to decide a target's outcome.
type Result struct {
	ID                    string        `json:"id"`
	Outcome               string        `json:"outcome"`
	Reason                string        `json:"reason,omitempty"`
	Resolution            string        `json:"resolution"`
	HistoricalImprovement bool          `json:"historical_improvement"`
	Application           string        `json:"application"`
	Observations          []Observation `json:"observations"`
}

// Observation retains one completed selected-test process attempt.
type Observation struct {
	Phase    string                     `json:"phase"`
	Status   mutator.Status             `json:"status"`
	Evidence *mutator.ExecutionEvidence `json:"evidence"`
}

// Report is the versioned verification document, separate from a run report.
type Report struct {
	SchemaVersion  int      `json:"schema_version"`
	Baseline       string   `json:"baseline"`
	Package        string   `json:"package"`
	Test           string   `json:"test"`
	DiscoveryScope string   `json:"discovery_scope"`
	Results        []Result `json:"results"`
}

// Run never modifies the checkout or Git metadata. Expected unsupported patches
// produce per-target inconclusive results; operational failures return errors.
func Run(ctx context.Context, options Options) (Report, error) {
	document := Report{SchemaVersion: 1, Package: options.Package, Test: options.Test, DiscoveryScope: "full active module; run.diff, coverage and dry-run gates not applied"}
	if err := validateSelector(options.Package, options.Test); err != nil {
		return document, err
	}
	if options.Baseline == "" || len(options.IDs) == 0 || len(options.IDs) > 10 || options.Timeout <= 0 || options.Timeout > 10*time.Minute {
		return document, fmt.Errorf("require baseline, 1..10 IDs and timeout in (0,10m]")
	}
	if err := report.ValidateMutantIDs(options.IDs); err != nil {
		return document, err
	}
	before, current, sha, err := snapshots(ctx, options.Root, options.Baseline)
	var patchErr error
	if err != nil {
		var unsupported unsupportedSnapshotError
		if !errors.As(err, &unsupported) {
			return document, err
		}
		patchErr = err
	}
	document.Baseline = sha
	existed := false
	if patchErr == nil {
		existed, patchErr = testPatch(before, current, options.Package, options.Test)
	}
	for _, id := range options.IDs {
		result := Result{ID: id, Outcome: outcomeInconclusive, Resolution: "not-resolved", Application: "not-applied", Observations: []Observation{}}
		if patchErr != nil {
			result.Reason = patchErr.Error()
			document.Results = append(document.Results, result)

			continue
		}
		result, err = verifyTarget(ctx, options, before, current, existed, id)
		if err != nil {
			return document, err
		}
		document.Results = append(document.Results, result)
	}

	return document, ctx.Err()
}

type discoveryOnly struct{}

func (discoveryOnly) Execute(_ context.Context, mutants []mutator.Mutator, _ func(mutator.Mutator)) ([]mutator.Mutator, error) {
	return mutants, nil
}

func verifyTarget(ctx context.Context, options Options, before, current snapshot, existed bool, id string) (Result, error) {
	result := Result{ID: id, Outcome: outcomeInconclusive, Application: "not-applied", Observations: []Observation{}}
	directory, err := os.MkdirTemp("", "imp-verify-discovery-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(directory)
	if err := current.write(directory); err != nil {
		return result, err
	}
	mod, err := gomodule.Init(directory)
	if err != nil {
		return result, err
	}
	discover := engine.New(mod, engine.CodeData{TargetIDs: []string{id}}, discoveryOnly{}, engine.WithVerificationSnapshot())
	discoveryContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	found, err := discover.Run(discoveryContext)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Resolution = "not-resolved"
		result.Reason = "active-build discovery failed in isolated snapshot"
		if discoveryContext.Err() == context.DeadlineExceeded {
			result.Outcome = outcomeTimeout
			result.Reason = "active-build discovery exceeded the attempt timeout"
		}

		return result, nil
	}
	result.Resolution = found.Replay[0].Resolution
	if result.Resolution != "matched" {
		switch result.Resolution {
		case "changed":
			result.Outcome = "stale"
		case "missing":
			result.Outcome = "removed"
		default:
			result.Outcome = outcomeInconclusive
		}
		result.Reason = "exact replay unresolved; missing means removed or unavailable in the active build"

		return result, nil
	}
	mutant := found.Mutants[0]
	// Observe all phases even after a red original; never claim improvement from it.
	phases := []struct {
		name    string
		source  snapshot
		mutated bool
	}{{"current-original", current, false}, {"current-mutant", current, true}}
	if existed {
		phases = append(phases, struct {
			name    string
			source  snapshot
			mutated bool
		}{"baseline-original", before, false}, struct {
			name    string
			source  snapshot
			mutated bool
		}{"baseline-mutant", before, true})
	}
	states := map[string]string{}
	for _, phase := range phases {
		for repeat := 0; repeat < 2; repeat++ {
			observation, err := observe(ctx, options, phase.name, phase.source, mutant, phase.mutated)
			if err != nil {
				return result, err
			}
			result.Observations = append(result.Observations, observation)
			if phase.mutated {
				result.Application = "exact mutant applied in isolated snapshots"
			}
			state := classify(observation, mod.Name, options)
			if repeat == 0 {
				states[phase.name] = state
			} else if states[phase.name] != state || !reflect.DeepEqual(result.Observations[len(result.Observations)-2].Evidence.FailingTests, observation.Evidence.FailingTests) {
				states[phase.name] = outcomeFlaky
			}
		}
	}
	for _, phase := range phases {
		if states[phase.name] == outcomeFlaky {
			result.Outcome = outcomeFlaky
			result.Reason = "repeated observations disagreed"

			return result, nil
		}
	}
	if existed && states["baseline-original"] != observedPass {
		result.Outcome = "baseline-failing"
		result.Reason = "original-code baseline did not pass; repair not established"

		return result, nil
	}
	if states["current-original"] != observedPass {
		result.Outcome = "original-failing"
		result.Reason = "current original did not pass selected test: " + states["current-original"]

		return result, nil
	}
	switch state := states["current-mutant"]; state {
	case "kill":
		result.Outcome = "detected"
		result.HistoricalImprovement = existed && states["baseline-mutant"] == observedPass
	case observedPass:
		result.Outcome = "ignored"
	case outcomeTimeout, "invalid", outcomeUnrelated:
		result.Outcome = state
	default:
		result.Outcome = outcomeInconclusive
		result.Reason = state
	}
	if result.Outcome == "detected" && !result.HistoricalImprovement {
		result.Reason = "detection observed; historical improvement not established"
	}

	return result, nil
}

func observe(ctx context.Context, options Options, phase string, source snapshot, mutant mutator.Mutator, mutated bool) (Observation, error) {
	result := Observation{Phase: phase}
	directory, err := os.MkdirTemp("", "imp-verify-attempt-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(directory)
	if err := source.write(directory); err != nil {
		return result, err
	}
	if mutated {
		mutant.SetWorkdir(directory)
		if err := mutant.Apply(); err != nil {
			return result, fmt.Errorf("apply exact target: %w", err)
		}
	}
	result.Status, result.Evidence = engine.ObserveSelectedTest(ctx, directory, options.Package, options.Test, options.Timeout)
	if mutated {
		if err := mutant.Rollback(); err != nil {
			return result, err
		}
	}

	return result, ctx.Err()
}

func classify(observation Observation, module string, options Options) string {
	evidence := observation.Evidence
	if evidence == nil {
		return "missing-evidence"
	}
	pkg := module
	if options.Package != "." {
		pkg += "/" + options.Package[2:]
	}
	switch observation.Status {
	case mutator.TimedOut:
		return outcomeTimeout
	case mutator.NotViable:
		if test := evidence.SelectedPassingTest; test != nil && test.Package == pkg && test.Test == options.Test {
			return outcomeUnrelated
		}
		if evidence.Category == "build-setup" {
			return "invalid"
		}

		return observedInfrastructure
	case mutator.Lived:
		if test := evidence.SelectedPassingTest; test != nil && test.Package == pkg && test.Test == options.Test {
			return observedPass
		}

		return "selected-test-not-observed"
	case mutator.Killed:
		if !evidence.JSONValid || evidence.FailingTestsTruncated || len(evidence.FailingTests) == 0 {
			return "unattributed"
		}
		for _, test := range evidence.FailingTests {
			if test.Package != pkg || (test.Test != options.Test && !isSubtest(test.Test, options.Test)) {
				return outcomeUnrelated
			}
		}

		return "kill"
	case mutator.NotCovered, mutator.Runnable, mutator.Skipped:
		return observedInfrastructure
	}

	return observedInfrastructure
}

func isSubtest(name, parent string) bool {
	return len(name) > len(parent) && name[:len(parent)+1] == parent+"/"
}
