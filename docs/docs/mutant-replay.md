# Exact mutant replay (identity v1)

```sh
imp run --dry-run --json --output findings.json
imp run --replay-report findings.json --json --output replay.json
imp run --mutant-ids 'imp-v1:…:…' --json
```

`--mutant-ids` accepts comma-separated IDs and may be repeated. Configuration
keys are `run.mutant-ids` (a list) and `run.replay-report` (a JSON file path).
Both selectors form a union; duplicates execute once. With neither selector,
normal discovery/execution is unchanged except canonical operator aliases now
produce one finding and one execution. The first enabled discovery operator
labels that finding; its label is **not** its identity.

## Minimal public contract for consumers

* JSON uses `schema_version: 2`, retaining existing fields and adding execution
  evidence. Replay accepts schema 1 and 2 reports with `identity_version: 1`,
  which announces replay support. Each finding has `id` and
  `identity` (`id`, `version`, `module_path`, `original`, `replacement`).
  `module_path` is the slash-separated **module-relative source file path**,
  not an import path. IDs are opaque, case-sensitive strings of the form
  `imp-v1:<64 lowercase hex>:<64 lowercase hex>`. Persist the entire ID.
* Identity includes the Go module name, module-relative file path, enclosing
  top-level declaration identity, the declaration's complete normalized Go
  token stream, mutation token index within that stream, and original and
  actual replacement text. It excludes absolute paths, line/column numbers,
  whitespace/comments, and operator labels. SHA-256 inputs use length framing.
  Function/method names (including receiver type) or grouped declaration names
  identify the enclosing declaration. Alias operators at the same source token
  with the same actual replacement collapse to one canonical edit. This includes
  both token edits and typed Boolean/error-return expression replacements; a
  Boolean literal return and its equivalent return-family edit share one ID.
* Unrelated line shifts and edits **outside** that declaration preserve IDs;
  whitespace/comment-only changes within it also preserve IDs when Go tokens
  are unchanged. Any token change inside the declaration conservatively
  invalidates its findings, even if unrelated to the target expression. This
  is source identity, not proof of semantic equivalence: changes to imports,
  other declarations, dependencies, tags, etc. can change behavior without
  changing an ID. Renames/moves are not followed. No fuzzy/line-number fallback.
* A prior report selects exactly the findings present in its `groups` (including
  any status filtering applied when it was written). Reports must have matching
  `go_module`, supported schema (1 or 2) and identity version (1), nonempty findings, and consistent
  finding IDs. Legacy reports without IDs, unknown versions, empty reports,
  malformed IDs, or multiple JSON documents are rejected. Prior statuses and
  test commands do not control the new execution.
* JSON `replay` is independent of status filtering and contains one entry per
  requested ID: `id` and `resolution`. Text reports print the same diagnostics.
  `matched` means exactly one canonical candidate; `changed` means its enclosing
  declaration still exists but no exact candidate does (including disabled
  operators); `missing` means no candidate/declaration is visible (deletion,
  rename/move, exclusion, or different discovery root); `ambiguous` means more
  than one candidate has the exact ID. Identical duplicate declarations, e.g.
  repeated identical `init` functions, are deliberately ambiguous. Hash
  collisions producing multiple candidates are also refused. A selector never
  chooses one arbitrarily.
* Exact-set resolution is atomic: if any target is changed/missing/ambiguous,
  **no mutation executes**, diagnostics are written, then the CLI returns an
  error. Matched diagnostics in that report do not imply execution. Invalid
  report/ID input fails before coverage. A resolved set is still subject to
  coverage, diff, dry-run, exclusions and current build/test scope.

## Scope and execution evidence

Replay does not override `--exclude-files`, generated-file exclusion,
`--include-files`, enabled operators, discovery path, `--diff`, `--tags`,
`--coverpkg`, integration mode or worker-copy exclusions. Excluded files are
not discovered and their IDs resolve as missing. Discovery only considers
active Go source files in the current build scope. Files inactive under the
current Go platform/tags are not discovered, so their token and typed-return IDs
resolve as `missing`, not `NOT COVERED`. Applicability-filtered targets resolve
as `changed` when their enclosing declaration remains visible. A matched
uncovered target is not forced to run; a target
outside diff scope stays `SKIPPED`. `--dry-run` never applies mutations. Set
compatible scope explicitly when replaying a prior report. Quality gates count
only the exact selected set, not the unselected module population.

Findings additionally expose `applied` (successful mutated-file write),
`applied_source_sha256` (SHA-256 of the complete bytes actually written),
`original_snippet`, `mutated_snippet`, and the existing `test_command` when an
execution was attempted. The snippets preview the exact token/expression edit; printer
formatting may differ in the applied file. `applied: true`, the source hash,
the mutant ID/replacement, command and terminal status together distinguish a
mutation attempt from merely running an unmutated package command. A command
alone is not evidence of an applied mutation. `KILLED`/`LIVED` indicate completed
tests; `NOT VIABLE`/`TIMED OUT` are attempts, not successful verification.
Uncovered/skipped/dry-run findings have `applied: false` and no applied hash or
command. Interrupted/lifecycle-failed runs return an error rather than a
successful report. Worker source bytes must still match discovery before Apply;
stale worker copies fail rather than receiving a mutation against other source.
The isolated worker copy is restored before results are returned.

The v1 contract is intentionally conservative; there is no cache, persistent
ledger, semantic matching, cross-file migration, or support for legacy position
replay. Future identity algorithms require a new identity version.
