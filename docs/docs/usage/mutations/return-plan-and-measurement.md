---
title: Return mutations — plan and measurement
---

# Return mutations: implementation plan and measurement

Issue [#49](https://github.com/smedijan/go-imp/issues/49) adds targeted return-site
mutations, not whole-function replacement or arbitrary tuple generation.

## Implementation plan and acceptance seams

1. Register independent `BOOLEAN_RETURN` and `ERROR_RETURN` categories, both default off.
2. Lazily load typed packages only when either operator is enabled. Honor calling directory,
   build tags, target sizes and language versions; use physical source positions. Fail closed
   for infrastructure failures or mismatched source snapshots. Skip type-invalid packages.
3. Walk explicit returns with the enclosing function's signature. Support explicit tuples
   and named explicit returns; skip bare returns and tuple forwarding. Match only built-in
   `bool` / `error` result types and aliases. Filter matching constants, nil and shadowed
   replacement names. Check candidate packages without rewriting unused declarations.
4. Splice only the selected expression. Preserve untouched bytes and expressions, snippets,
   coverage/diff selection, isolated worker execution and exact rollback. The replaced
   expression is deliberately not evaluated.
5. Validate safety and outcomes before choosing defaults. Keep both operators opt-in until
   broader real-world usefulness and cost evidence exists.

Source-named tests keep the acceptance seams discoverable:

- `internal/engine/return_analysis_test.go`: aliases, tuples, named/bare returns, nested
  literals, shadowing, constant/nil filtering, unused declarations/imports, loader contract,
  snippets, physical positions, coverage/diff selection and Boolean-literal overlap.
- `internal/engine/return_loader_test.go`: build tags, calling directory, cancellation,
  dependency/source errors, target sizes and language versions.
- `internal/engine/returnmutator_test.go`: exact splices, repeated application/rollback,
  stale-source rejection, failed restoration retry and concurrent isolated copies.
- `internal/engine/return_evaluation_test.go`: real race-enabled Go tests prove replaced
  expressions are not evaluated while untouched result order and initialization survive.
- `internal/engine/return_acceptance_test.go`: actual engine/executor outcomes in a reused
  sequential worker; exact source restoration; integrated coverage/diff and snippet checks.
- `cmd/return_acceptance_test.go`: build the CLI once into a temporary directory, then run
  baseline, each category alone, combined and combined dry discovery against a fixed module.
  All categories are explicitly toggled, so no existing operator contaminates the counts.

## Fixed fixture and protocol

`internal/engine/testdata/returns` is a standalone, dependency-free module with exactly:

```go
func AssertedFlag(flag bool) bool { return flag }
func IgnoredFlag(flag bool) bool { return flag }
func CaughtError(err error) error { return err }
func IgnoredError(err error) error { return err }
```

Tests assert both true and false flags and `errors.Is` preservation of a nonnil error. Other calls execute
but ignore their results. Parameter replacements do not introduce unused locals/imports.
The acceptance test copies the module to a temporary directory and checks all original Go
files and `go.mod` are byte-identical after every CLI invocation. `--workers=1` forces reuse
of one worker, exercising restoration between mutations.

Reproduce with:

```shell
go test -race ./cmd -run TestReturnCLIQuality -v
go test -race ./internal/engine -run TestReturnAcceptance -v
```

The test generates `--<operator>=false` for **every** category from `mutator.Types`, then
sets only the requested return categories to true. Each run uses `--json --quiet --workers=1`
and an empty `.imp.yaml`. The CLI build is outside measured invocation time.

## Observed measurements

Local Linux run after fixture lint cleanup, Go 1.27.3, warm shared Go build cache, single worker. Times are one sample,
not a performance benchmark. Engine elapsed includes discovery and mutation execution;
wall time also includes CLI startup, module/coverage analysis and worker preparation.
Dry engine elapsed approximates discovery cost, not a separately instrumented loader time.

| Operators | Total | Killed | Lived | Runnable | Not viable | Engine seconds | Wall seconds |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| All disabled | 0 | 0 | 0 | 0 | 0 | 0.000551 | 0.433 |
| Boolean only | 4 | 2 | 2 | 0 | 0 | 1.465096 | 1.791 |
| Error only | 2 | 1 | 1 | 0 | 0 | 0.724773 | 1.068 |
| Combined | 6 | 3 | 3 | 0 | 0 | 2.054587 | 2.397 |
| Combined dry | 6 | 0 | 0 | 6 | 0 | 0.023564 | 0.318 |

All runs had zero uncovered, skipped or timed-out mutants. Each executed category had 50%
efficacy and 100% mutation coverage. All three deliberately asserted-result mutations were
killed; all three deliberately ignored-result mutations lived. There were no mechanically
identical candidates in this fixture. No build-invalid mutant was observed; filtering and
build-failure classification have separate safety tests.

The API acceptance run additionally measured 3 killed / 3 lived in 2.450 seconds, then a
selected dry run with 2 runnable / 2 not covered / 2 skipped in 0.032 seconds. Original
sources and tests remained byte-identical. This small designed fixture establishes expected
behavior, not equivalence rates or quality across production modules. It is insufficient
evidence to enable either operator by default.

## Validation and review follow-up

The acceptance chunk passed `hk check --all` (including nested fixture lint),
`go test -race ./...`, focused return/CLI race tests,
`golangci-lint run ./cmd/... ./internal/engine/...`, `rumdl check` on the six changed
Markdown pages, Draft 7 schema validation (including boolean option defaults and rejection
of string values), and `uvx zensical build -f docs/mkdocs.yml`.

Core review identified a loader correctness gap: `go/packages` clears module metadata
unless `packages.NeedModule` is requested. The loader now explicitly requests it. A real
Go 1.23 fixture verifies the loaded language version; a Go 1.21 module regression verifies
that candidate checking rejects Go 1.22 integer-range syntax (and accepts the control when
checked as Go 1.22). This preserves the module language constraint independently of any
file-level build constraint.
