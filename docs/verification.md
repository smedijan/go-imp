# Verifying a test patch

`imp verify` generates evidence, not tests or a semantic oracle. Run at a Git
repository root that is also the Go module root:

```sh
imp verify --baseline HEAD --package ./calculator --test TestBoundary \
  --mutant-ids 'imp-v1:…:…' --output /tmp/verification.json
# Alternatively select the findings present in a prior identity-aware report:
imp verify --baseline HEAD --package . --test TestBoundary \
  --replay-report /tmp/survivors.json
```

Use complete IDs from `imp run --json`. No line/operator fallback is attempted. The explicit baseline is resolved to a
commit and recorded. Current means tracked working-tree files plus nonignored untracked files (not the index). If any
ignored files exist, verification is inconclusive: they might be Go sources, embedded assets or runtime inputs.
Relocate/remove ignored artifacts yourself before verification; imp never deletes them or silently omits them from a
successful run. Save reports outside the repository: a new report inside it is itself a non-test patch on the next
invocation.

## Deliberately narrow support

Select **one literal package directory** (`.` or `./directory`) and **one named
top-level Test function**, not a regular expression, wildcard, or subtest selector.
Failures retain named subtest paths under that test. The patch must change only
that function, or add it to an existing test file, or add a new test-only file
containing imports and that function. Exactly one test file may change. Existing
imports, helpers, TestMain, other tests, test data, production files, module files,
file removals/mode changes, symlinks, submodules and local module replacements are
unsupported. Unsupported/ambiguous patch attribution is inconclusive, not a kill.
This conservative boundary avoids attributing a changed shared helper to a test.

Verification intentionally discovers the **full active module**, independent of
normal `run.diff` (including a configured default), coverage eligibility, dry-run,
run report filters and worker settings. It reuses exact identity/replay resolution
and the configured mutation families/active Go build. It does not run a coverage
baseline. Missing targets mean removed **or unavailable in that active build**;
changed declaration identities are stale. These are not silently skipped.

## Observations and outcomes

At most ten distinct targets are accepted. For each resolved target, two fresh
snapshot attempts run each applicable phase: current original, current exact
mutant, and (if the selected test existed) baseline original and baseline mutant.
`go test -json -count=1 -run '^TestName$'` selects the test, with a per-attempt
wall timeout (default 30s, maximum 10m). Each phase is isolated from previous test
filesystem writes. The normal executor owns process cleanup, failure classification
and bounded/redacted diagnostics. JSON includes every phase's command, passing and
failing test/subtest names, diagnostics, timeouts and process exit evidence.
Passing-test lists retain at most 100 events and expose truncation. The selected
top-level completion is retained independently, so a long prefix of passing
subtests cannot hide a valid selected-test pass.

`detected` requires repeated observed original passes and mutant failures attributed
only to the selected test/subtests. `historical_improvement` is true only when the
existing baseline test also repeatedly passed original code and ignored this exact
mutant. A new test has no observed baseline test: detection may be established but
historical improvement is explicitly **not** established. A baseline that already
detected the mutant also does not establish improvement. Runtime observations do
not prove requirements or semantic correctness.

Other outcomes include `ignored`, `baseline-failing`, `original-failing`, `invalid`,
`timeout`, `unrelated`, `flaky`, `stale`, `removed`, and `inconclusive`. Failure of an
original snapshot (including Git-dependent setup failure) never becomes a repair.
Different repeated classifications or failing test lists produce `flaky`. Two
matching observations are **not an absolute guarantee against flakiness**; hidden
external state and rare instability remain possible. Test code itself is trusted,
not sandboxed; networks and external side effects are not isolated.

## Safety and exits

Git commands against the original are explicitly rooted, read-only and strip all
inherited `GIT_*` state. Snapshots contain **no `.git` directory or linked-worktree
pointer**, and never write an index, ref, worktree or shared Git configuration.
Before making proof claims, a rooted `go env GOWORK` query preserves the original
workspace context while stripping inherited `GIT_*` state. Any active workspace,
including an implicit parent or explicit external `GOWORK`, is unsupported and
returns inconclusive. Workspace inputs are never materialized. Once this
self-contained-module precondition passes, snapshot subprocesses strip inherited
`GIT_*` state and disable workspace lookup. Tests needing repository metadata may
fail original setup and therefore
cannot establish detection with this workflow. No general mutation-worker copy
behavior is changed.

Only all-`detected` results exit 0 (inspect `historical_improvement` separately).
Any other per-target result publishes the complete JSON and exits 1. Invalid
arguments or operational errors return nonzero without publishing a partial file;
Cobra flag/argument errors exit 2. Report files reuse atomic staging/replacement:
operational failure or cancellation leaves a prior report untouched. Stdout cannot
be rolled back after a stream write failure. Normal `imp run` behavior is unchanged.
