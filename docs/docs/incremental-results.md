# Incremental mutation results

Disabled by default. Opt in for repeated runs of a **hermetic** Go module:

```sh
imp run --incremental --json
imp run --incremental --json  # second independent fresh observation
imp run --incremental --json  # reuse agreeing killed/lived outcomes
imp run --incremental --incremental-refresh  # discard evidence, start fresh
imp run --incremental=false  # neither read nor write the ledger
```

The corresponding YAML keys are `run.incremental`, `run.incremental-refresh`,
`run.incremental-ttl-hours` (default 24, range 1–720), and
`run.incremental-max-entries` (default 2000, range 1–10000). Environment variables
use the normal `IMP_RUN_...` spelling. Refresh requires incremental mode; dry
runs do not consult or populate the ledger. Leaving refresh enabled forces every
run fresh. Baseline tests and coverage **always** run, including fully reused
runs. Fresh mutant tests retain `go test -count=1`; independent observations are
separate successfully completed runs, not Go's test-result cache.

## Validity and confidence

Stable mutant IDs identify edits, not behavior. Reuse additionally requires the
same broad SHA-256 fingerprint of:

- Every regular file in the entire module, workspace directory (when active),
  and all dependency module directories, including external `replace` targets.
  Tests, helpers, non-Go files, hidden files, fixtures, embed inputs, module
  manifests, otherwise inactive sources, and repository metadata inside these
  directories are included; no source-path exclusions are applied. File content,
  mode and modification time, plus directory mode and modification time, are
  fingerprinted; timestamp-only fixture edits invalidate outcomes too.
- The complete selected Go toolchain directory, Go version/settings, imp binary,
  effective mutation/execution configuration, calling scope and runtime CPU settings.
- The sorted process environment, including flags and potential secrets. Only
  the resulting hash is persisted, **never raw environment values**.

Any such change invalidates survivors **and kills**. Dependency metadata failure, nested symlinks/junctions or
nonregular inputs, cgo, nonstandard assembly, and unsupported `GOFLAGS` disable reuse and persistence for that run, with
a report note. Supported `GOFLAGS` are `-p=`, `-tags=`, `-race`, `-trimpath`, `-mod=`, and `-buildvcs=`; input-bearing
options such as overlays, external modfiles and tool executors conservatively bypass reuse. Cgo's generated temporary
`GOGCCFLAGS` is not hashed because cgo execution is unsupported. Explicitly selected module, workspace, dependency and
toolchain root directories are canonicalized (`EvalSymlinks` on non-Windows; on Windows, a directory handle's normalized
final DOS/UNC path, including GOROOT junctions). Both the original selection and canonical root path are hashed, along
with the canonical tree; retargeting a root invalidates evidence. Links encountered **inside** any tree remain
unsupported and are never followed. Ledger overlap is refused for both selected and resolved paths, including
cache-directory aliases. No dependency graph narrowing is attempted.

This is not a sandbox or a proof of deterministic tests. Network services,
clocks, random inputs, external repository metadata and files outside these roots cannot
be validated: **leave incremental mode disabled for tests that depend on them**.
Fingerprints describe the **original input tree**, not generated worker copies.
Worker isolation currently creates fresh file/directory timestamps rather than
preserving original timestamps. Tests reading original fixture metadata through
stable original paths are covered by timestamp invalidation; tests whose behavior
depends on worker-copy creation/modification times are not hermetic and must keep
incremental mode disabled. Access times, root-alias metadata and other untracked filesystem metadata
are likewise unsupported inputs. Refresh after changing external state.
Whole-tree hashing is intentionally
expensive and may outweigh savings on small suites. Reports stored inside an
input tree are hashed like any other file and can prevent reuse; put reports
outside these trees or use stdout. The ledger itself is stored externally, not
excluded using broad source-path patterns. A cache directory inside an input tree
is refused.

Only `KILLED` and `LIVED` can be reused, after at least two agreeing actual fresh
observations under the identical fingerprint. Timeouts, invalid builds, skipped,
uncovered, dry-run and incomplete outcomes are never reusable. Observed outcome
disagreement under an identical fingerprint sets a **ledger-wide sticky
inconclusive marker**, surviving TTL/entry eviction: no outcome is reused until
explicit refresh. This deliberately trades hit rate for safety. Baseline failure
or incomplete/cancelled execution does not publish observations.

## Evidence and transitions

JSON adds an `evidence` array, independently of finding-status filtering, with
`id`, `provenance` (`fresh`, `reused`, `stale`), `confidence` (`unconfirmed`,
`confirmed`, `inconclusive`, `historical`), `scope`, `confirmations`, and optional
`previous` historical status and `transition`. Text reports print the same
information. Reused findings have no current applied-file hash or test command:
no mutation was applied this run. Existing counts include valid reused outcomes;
historical-only records never contribute to counts or quality gates.

Transitions include `lived-to-killed`, `killed-to-lived`, `unchanged`, `changed`
(the declaration is still visible but the edit is not), `removed` (no visible
declaration), and `unresolved` (nonconclusive current outcome, ambiguous ID, or
outside exact replay scope). Changes of scope, exclusion, or enabled operators
can make a finding changed/removed without a source deletion. History is bounded,
so evicted/expired findings have no transition evidence. Prior evidence is never
presented as current execution. Fingerprints are checked before baseline,
after discovery/before reuse, and before publication; a detected concurrent input
change aborts the incremental run rather than reporting stale kills as current.

## Storage and recovery

Version 1 ledgers live under the OS user cache directory, in
`imp/outcomes-v1/<SHA256-of-absolute-module-root>.json`. Retention is per module:
TTL measured from actual observations (reuse does not extend it), configured entry
cap, and a hard 4 MiB file/read limit. A private temporary sibling is synced then
atomically renamed; partial files never replace the last ledger. Corrupt or
unknown versions bypass reuse/persistence until `--incremental-refresh`.

A per-module exclusive lock is held through baseline and mutant execution.
Concurrent contenders execute fresh and skip persistence, never overwrite the
owner's evidence, and do not remove its lock. No global lock is used. A process
crash may leave `<ledger>.lock`; after ensuring no owner is running, remove that
lock manually. A failed/cancelled refresh preserves the previous ledger.

## Measuring iterative workflows

Run the eight-mutant end-to-end benchmark (baseline, discovery, full input
fingerprinting and fresh versus confirmed reuse), or the isolated ledger I/O
benchmark:

```sh
go test -race ./cmd -run '^$' -bench '^BenchmarkCLIIncrementalWorkflow$' -benchtime=1x
go test -race ./internal/ledger -run '^$' -bench '^BenchmarkLedgerRepeatedWorkflow$'
```

These intentionally retain all baseline and fingerprint overhead. Report measured
results for your own module; a reusable result is not a promise of a speedup.
