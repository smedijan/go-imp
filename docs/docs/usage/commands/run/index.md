# Run

`imp run [path]` tests how effectively a Go module's tests detect mutations. `imp r`
is a shorthand. Without a path, imp uses the current directory.

```shell
imp run
imp run --statuses lived,not-covered
imp run --statuses killed --verbose
imp run --json | jq '.groups.LIVED'
imp run --json --statuses lived --output survivors.json
imp run --dry-run --json
```

Help groups flags into **Reporting**, **Scope**, **Execution**, **Quality gates**
and **Mutation operators**. Run `imp run --help` for the full flag list.

## Reporting

### Human output

The default report groups findings by status, then file, with source locations and
mutation operators. All nonempty status groups are included, in triage order:
survivors, uncovered code, timeouts, runnable mutants, killed, skipped and invalid mutants.

Short source diffs are shown for survivors and uncovered code. Survivor test commands
are included when available. Use `--verbose` / `-v` to expand source diffs and test
commands for every selected status. Commands describe package or integration scope,
build tags, CPU and timeout options; they do not attribute results to individual tests.

Counts for every status and efficacy/coverage rates describe the whole run. Survivors
require judgment: equivalent mutations and intentional behavior are possible.

### JSON

`--json` selects grouped JSON instead of human-readable text. It writes exactly one
JSON document, without progress messages, ANSI escapes or source snippets.

```shell
imp run --json --statuses lived,not-covered | jq '.groups'
```

The schema has `schema_version: 1`. Findings are grouped by status and then filename,
so status and filename are not repeated on every mutation. `test_command` is a string
and is omitted when unavailable. The example below selects only survivors:

```json
{
  "schema_version": 1,
  "go_module": "example.com/pkg",
  "dry_run": false,
  "selected_statuses": ["lived"],
  "groups": {
    "LIVED": {
      "example.go": [
        {
          "type": "CONDITIONALS_BOUNDARY",
          "line": 12,
          "column": 9,
          "test_command": "go test -timeout 7s -failfast example.com/pkg"
        }
      ]
    }
  },
  "test_efficacy": 50,
  "mutations_coverage": 66.66666666666666,
  "mutants_total": 3,
  "mutants_killed": 1,
  "mutants_lived": 1,
  "mutants_not_viable": 0,
  "mutants_not_covered": 1,
  "mutants_timed_out": 0,
  "mutants_skipped": 0,
  "mutants_runnable": 0,
  "elapsed_time": 2.345,
  "mutator_statistics": {
    "arithmetic_base": 1,
    "conditionals_boundary": 1,
    "conditionals_negation": 1
  }
}
```

JSON object key order is not significant. Findings within each file are sorted by
location and operator, independent of worker completion order. Elapsed time is in
seconds. Empty or unmatched results produce a valid report with `"groups": {}`.

### Status filtering

`--statuses LIST` selects findings in **every output format and destination**. The
comma-separated names are explicit rather than abbreviated letters. Default: all statuses.

| Flag value | Report group | Meaning |
| --- | --- | --- |
| `lived` | `LIVED` | Tests passed with the mutation |
| `not-covered` | `NOT COVERED` | Coverage did not exercise the code; mutation not tested |
| `timed-out` | `TIMED OUT` | Test execution exceeded the timeout |
| `runnable` | `RUNNABLE` | Covered but not tested, for example during a dry run |
| `killed` | `KILLED` | Tests detected the mutation |
| `skipped` | `SKIPPED` | Outside the selected diff scope |
| `not-viable` | `NOT VIABLE` | Mutation could not be built or executed as valid tests |

```shell
imp run --statuses lived,not-covered
imp run --json --statuses killed --output killed.json
```

Filtering does not change execution, aggregate metrics or quality gates. JSON includes
`selected_statuses`, even when no findings match. Unknown names fail before execution.

### Destination

`--output FILE` / `-o FILE` writes the **selected format** to a file instead of stdout.
It does not choose a format based on the extension. Omit the flag or use `--output -`
for stdout. A file named `survivors.json` still needs `--json` to contain JSON.

```shell
imp run --output report.txt
imp run --json --statuses lived --output survivors.json
imp run --json --output -
```

Progress and errors go to stderr, never the report destination. Output failures return
an error rather than silently discarding results.

### Quiet and color

`--quiet` / `-q` suppresses progress, not the report or errors. Progress is only shown
when stderr is a terminal. Redirected reports never contain color; `--no-color`,
`NO_COLOR` and `TERM=dumb` also disable it.

```shell
imp run --json --quiet > results.json
```

### Migration

Grouped reporting is the default, not an experiment. The old file/mutation JSON schema
has been replaced by the versioned grouped schema. Replace `--output-statuses lc` with
`--statuses lived,not-covered`; use `--verbose` instead of `--output-diff-statuses`.
Replace `--silent` with `--quiet`, noting that quiet no longer suppresses the report.
Use `--json --output FILE` where `--output FILE` previously implied JSON.

## Scope

| Flag | Purpose |
| --- | --- |
| `--diff REF` / `-D REF` | Test mutations in changes against the merge base with a branch or commit |
| `--tags LIST` / `-t LIST` | Comma-separated Go build tags |
| `--coverpkg LIST` | Comma-separated package patterns for coverage analysis |
| `--exclude-files REGEXP` / `-E REGEXP` | Exclude matching paths; repeat for multiple rules |

```shell
imp run --diff origin/main
imp run --tags tag1,tag2 --coverpkg './internal/...,./pkg/...'
imp run -E '_(gen|wrap).go$' -E '^generated/'
```

Test files are always excluded from mutation discovery. Excluded files are omitted
from threshold calculations. Diff filtering marks out-of-scope mutants as `SKIPPED`;
the baseline test run is unchanged. Fetch the comparison ref before using `--diff`.

## Execution

| Flag | Default | Purpose |
| --- | --- | --- |
| `--dry-run` / `-d` | `false` | Gather coverage and discover mutants without testing them |
| `--integration` / `-i` | `false` | Run the complete module test suite for each mutation |
| `--workers N` | `0` | Mutation workers; zero selects the CPU count (half in integration mode) |
| `--test-cpu N` | `0` | Set the Go test CPU option; zero leaves it unset |
| `--timeout-coefficient N` | `0` | Timeout multiplier; zero uses the default of five |

### Integration mode

Normally imp tests only the package containing the mutation. Integration mode catches
cross-package effects at the cost of slower runs. See [workers](workers.md) for CPU
and concurrency details.

### Timeouts

Each mutation's timeout is `max(coverage duration, 1 second) × coefficient`. For example,
a 500 ms coverage run with coefficient five gets a five-second timeout. Timing-sensitive
tests and system load can cause small variations between runs.

## Quality gates and exit codes

| Flag | Calculation |
| --- | --- |
| `--threshold-efficacy PERCENT` | `KILLED / (KILLED + LIVED) × 100` |
| `--threshold-mcover PERCENT` | `(KILLED + LIVED) / (KILLED + LIVED + NOT COVERED) × 100` |

Defaults are zero (disabled). A nonzero threshold fails if the rate is at or below it.
The full selected report is emitted before a gate failure. Dry runs do not enforce gates;
their coverage rate is `RUNNABLE / (RUNNABLE + NOT COVERED) × 100`.

| Exit code | Meaning |
| --- | --- |
| `0` | Completed successfully |
| `1` | Execution or output failure |
| `2` | Invalid flags, arguments or configuration |
| `10` | Efficacy threshold not met |
| `11` | Mutant coverage threshold not met |
| `130` | Interrupted |

## Mutation operators

Each flag enables its operator; append `=false` to disable it.

| Flag | Default | Details |
| --- | --- | --- |
| `--arithmetic-base` | `true` | [Arithmetic base](../../mutations/arithmetic_base.md) |
| `--conditionals-boundary` | `true` | [Conditional boundaries](../../mutations/conditionals_boundary.md) |
| `--conditionals-negation` | `true` | [Conditional negation](../../mutations/conditionals_negation.md) |
| `--increment-decrement` | `true` | [Increment/decrement](../../mutations/increment_decrement.md) |
| `--invert-negatives` | `true` | [Negative signs](../../mutations/invert_negatives.md) |
| `--invert-assignments` | `false` | [Assignments](../../mutations/invert_assignments.md) |
| `--invert-bitwise` | `false` | [Bitwise operators](../../mutations/invert_bitwise.md) |
| `--invert-bwassign` | `false` | [Bitwise assignments](../../mutations/invert_bitwise_assignments.md) |
| `--invert-logical` | `false` | [Logical operators](../../mutations/invert_logical.md) |
| `--invert-loopctrl` | `false` | [Loop control](../../mutations/invert_loop.md) |
| `--remove-self-assignments` | `false` | [Self-assignments](../../mutations/remove_self_assignments.md) |
