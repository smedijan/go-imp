package engine

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/mutator"
)

// WithVerificationSnapshot isolates Go discovery from inherited Git routing and
// workspace lookup. It is only for metadata-free, caller-owned snapshots.
func WithVerificationSnapshot() Option {
	return func(m Engine) Engine {
		m.verificationSnapshot = true

		return m
	}
}

func verificationEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") && !strings.HasPrefix(entry, "GOWORK=") {
			env = append(env, entry)
		}
	}

	return append(env, "GOWORK=off")
}

// ObserveSelectedTest uses the normal executor's process lifecycle, bounded
// diagnostics and classification, but runs one literal named top-level test.
// The caller owns an isolated directory; no discovery/coverage gates apply.
func ObserveSelectedTest(ctx context.Context, directory, pkg, test string, timeout time.Duration) (mutator.Status, *mutator.ExecutionEvidence) {
	mutant := &TokenMutator{}
	mutant.SetWorkdir(directory)
	executor := &MutantExecutor{execContext: exec.CommandContext, testExecutionTime: timeout, buildTags: configuration.Get[string](configuration.RunTagsKey), selectedTest: test, verificationSnapshot: true}
	attempt := mutantExecutor{executor: executor, mutant: mutant}
	status := attempt.runTests(ctx, directory, pkg)

	return status, mutant.ExecutionEvidence()
}
