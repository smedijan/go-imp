# Configuration

imp can be configured via (in order of precedence):

1. command flags
2. environment variables
3. configuration files

## Command flags

Flags have the higher priority and override all the other means of setting behaviours. Please refer to the specific
command documentation to learn how to use them.

## Configuration files

imp can be configured with a configuration file.

### Location

The configuration file can be placed in (in order of precedence)

1. `./.imp.yaml` (the current directory)
2. The module root
3. `/etc/imp/.imp.yaml`
4. `$XDG_CONFIG_HOME/imp/.imp.yaml`
5. `$HOME/.imp.yaml`

[//]: # (@formatter:off)
!!! hint
    `XDG_CONFIG_HOME` is usually `~/.config`.

[//]: # (@formatter:on)

### Override

The config file can be overridden with the `--config` flag.

```shell
imp run --config=myConfig.yaml
```

### Reference

Here is a complete configuration file with all the properties set to their defaults:

```yaml
quiet: false
no-color: false
run:
  integration: false
  dry-run: false
  confirm-outcomes: false
  tags: ""
  output: ""
  json: false
  verbose: false
  statuses: ""
  diff: ""
  diff-scope: changed-line
  workers: 0 #(1)
  test-cpu: 0 #(2)
  timeout-coefficient: 0 #(3)
  threshold: #(4)
    efficacy: 0
    mutant-coverage: 0
  exclude-files: [] #(5)
  include-files: [] #(6)
  exclude-workdir: [] #(7)

mutants:
  arithmetic-base:
    enabled: true
  conditionals-boundary:
    enabled: true
  conditionals-negation:
    enabled: true
  boolean-literal:
    enabled: false
  boolean-return:
    enabled: false
  error-return:
    enabled: false
  increment-decrement:
    enabled: true
  invert-assignments:
    enabled: false
  invert-bitwise:
    enabled: false
  invert-bwassign:
    enabled: false
  invert-negatives:
    enabled: true
  invert-logical:
    enabled: false
  invert-loopctrl:
    enabled: false
  remove-self-assignments:
    enabled: false

```

1. By default `0`, which means that imp will use the system CPUs number.
2. By default `0`, which means that no test process CPU will be enforced.
3. By default `0`, which means a default coefficient of `5` will be enforced.
4. Thresholds are set by default to `0`, which means they are not enforced.
5. Excluded files are set by default to empty list; generated Go files are still excluded automatically.
6. Include patterns override generated-file detection; explicit exclusions take precedence.
7. Worker-copy exclusions are empty by default. Set module-relative file or directory paths to omit them from worker
   copies; directories are pruned recursively. This is independent of `.gitignore`, which does not affect copies.
   Excluded assets may be required by builds or tests.

For further information check the specific command documentation.

[//]: # (@formatter:off)
!!! tip
    You can validate the configuration file using the provided JSON Schema (ex. using it in your editor). The schema
    can be found at [the configuration schema](../schema/configuration.json).

[//]: # (@formatter:on)

## Reporting

Grouped reporting is always enabled. Format, destination and status filtering are independent:

```yaml
quiet: true
run:
  json: true
  output: survivors.json
  statuses: lived,not-covered
```

This writes filtered JSON to `survivors.json` with no report on stdout. Set `json: false`
for human text; filenames do not choose the format. Empty output or `-` means stdout.
`statuses` is a comma-separated string; empty selects all statuses. Counts and quality
gates still describe the whole run. `quiet` suppresses only progress, never the report
or errors. `verbose` expands human source context for all selected statuses.

Equivalent environment variables include `IMP_RUN_JSON=true`,
`IMP_RUN_OUTPUT=survivors.json`, `IMP_RUN_STATUSES=lived,not-covered`, `IMP_QUIET=true`
and `IMP_NO_COLOR=true`. Explicit command flags override environment and configuration,
including `--json=false` and `--statuses=`.

See [reporting](commands/run/index.md#reporting) for the output contract.

## Environment variables

imp can be configured via environment variables as well. You can construct the variable name referring to the
configuration file format. They start with `IMP_`, and each dot and dash becomes an underscore.

For example:

```yaml
mutants:
  arithmetic-base:
    enabled: true
```

Can be set with:

```shell
export IMP_MUTANTS_ARITHMETIC_BASE_ENABLED=true
```

## Optional outcome confirmation

`imp run --confirm-outcomes` (or `run.confirm-outcomes: true`,
`IMP_RUN_CONFIRM_OUTCOMES=true`) enables strict verification of **killed and
timed-out outcomes only**. It is off by default, ignored for dry runs, and does
not repeat passing, uncovered, skipped, or infrastructure-failed mutants.

Before any workers mutate or execute tests, imp captures an isolated pristine
module snapshot, honoring workdir exclusions and removing Git metadata (including
worktree pointer files). Later checkout edits and test-created worker files are
not sources for confirmation. A suspicious outcome first runs an unmutated copy;
only a passing baseline permits one fresh mutant repeat. Each gets a separate
copy of the pristine snapshot. Package/integration scope, build tags, CPU setting,
test arguments, inherited environment and temporary-build root match the original
attempt. Tests that require Git metadata are not supported by confirmation.
External services, absolute file paths, symlinks (not copied), and external module
replacements are **not** isolated by a directory snapshot.

There are at most **three total process attempts per suspicious mutant** (initial,
baseline, repeat). Confirmation shares a deadline of twice the existing mutant
attempt timeout; individual attempts retain that timeout. Copy traversal checks
cancellation too; filesystem operations and mandatory rollback/cleanup can add
wall time. No global timeout is increased, and no package-specific timing policy
is introduced (tracked separately in #13). Cancellation restores active mutations
before returning, without publishing interrupted results.

JSON execution evidence preserves the original observation and adds `attempts`
with roles, timings, failing tests and bounded/redacted diagnostics, plus an
optional `confidence`:

| Confidence | Meaning |
| --- | --- |
| `confirmed` | Initial mutant failed tests, pristine baseline passed, fresh mutant failed tests again. |
| `baseline-unstable` | Unmutated tests failed; mutant causality is not established. |
| `inconsistent` | Repeat does not reproduce the initial test-failure outcome (including recovery after a timeout). |
| `infrastructure` | Build/setup, launch or other non-test failure; not a kill. |
| `unresolved-timeout` | Confirmation timed out or exhausted its budget; never a confirmed kill. |

Human findings show the confidence reason. For compatibility, unconfirmed kills
use `NOT VIABLE`; original timeouts remain `TIMED OUT`. Neither counts as killed
or conclusive. If opt-in evidence exists but there are no killed/lived results,
efficacy is **0**, not the legacy empty-set 100; this is a conservative gate value,
not an estimate of statistical confidence. Existing positive efficacy thresholds
therefore fail using their existing exit codes. Coverage keeps its existing
meaning/calculation; unknown outcomes alone do not zero coverage. Default-mode
scores and exits are unchanged. `ExecutionEvidence.ReusableDetection()` explicitly
rejects missing/unverified, unstable, inconsistent and timeout observations; no
cache is implemented.

A single baseline and repeat cannot prove a suite is flake-free; confirmation is
bounded evidence, not a statistical guarantee or failing-test attribution proof.
A confirmed outcome establishes repeated test failure, not necessarily the same
individual failing test. No known-flaky-test registry or broad retry is included.

### Cost measurement

On the deterministic tiny real-Go fixture (`TestExecutorConfirmationRealMutation`,
Linux, `GOMAXPROCS=2 GOFLAGS=-p=2`, race-enabled harness), package mode measured
0.292s initial + 0.284s baseline + 0.258s repeat (0.870s elapsed); integration mode
measured 0.328s + 0.341s + 0.295s (1.005s elapsed). Thus a confirmed kill costs
roughly **three test runs instead of one**, plus snapshot/copy I/O. Baseline failure
short-circuits after two attempts. Ordinary passing mutants still cost one run;
opt-in campaigns additionally pay one pristine snapshot per campaign and up to
two transient source copies per active suspicious worker. These measurements
are illustrative, not performance guarantees.
