# Persistent human triage (version 1)

Triage is **report-only**. It never changes discovery, execution, statuses,
coverage, efficacy, or whole-run quality gates. Suspected equivalence is a human
hypothesis, not proven equivalence and never `KILLED`. A successful triage write
is not a waiver. Default reports remain unfiltered and ungrouped.

## Workflow and human-authored format

1. Run `imp run --json --output findings.json` (or `--dry-run` to inventory).
2. Create a separate JSON ledger. Copy each reviewed finding's **entire identity**
   object, not its line number or operator label. Set a disposition, a substantive
   reason, and provenance identifying the reviewer and evidence (for example a
   dated issue/review URL). No decisions are generated automatically.
3. Run `imp run --triage-input triage.json --triage-output triage-next.json
   --json --output reviewed.json`. The annotated ledger is saved atomically to a
   distinct file; inspect it and deliberately replace the old ledger yourself.
   `--triage-output` without input writes an empty version-1 ledger template.
4. Optionally add `--hide-reviewed` and/or `--group-review`. Omit these flags to
   recover the full ungrouped findings. These are presentation controls only.

Example ledger (replace the placeholder identity with a real report identity):

```json
{
  "version": 1,
  "module": "example.com/service",
  "entries": [
    {
      "identity": {
        "id": "imp-v1:<64 lowercase hex>:<64 lowercase hex>",
        "version": 1,
        "module_path": "calc.go",
        "original": ">",
        "replacement": ">="
      },
      "disposition": "accepted-gap",
      "reason": "Boundary assertion deferred until the specified contract is settled.",
      "provenance": "Kaj; review URL or issue #123; 2026-05-01"
    }
  ]
}
```

Allowed dispositions: `intentional-behavior`, `suspected-equivalence`,
`accepted-gap`, `needs-clarification`. Every entry requires a nonblank reason
and provenance. `needs-clarification` remains visible even with `--hide-reviewed`.
The ledger version, module, identities, fields, decisions, duplicate keys/anchors,
and single-document JSON syntax are strictly validated. Unknown or malformed
input fails before coverage, never becoming a waiver. Annotations may include
`resolution`; this is always recomputed, never trusted from input.

## Anchoring and review after changes

The [canonical v1 identity](mutant-replay.md) is the source fingerprint: its ID
includes the full normalized enclosing declaration and the precise edit index.
Line shifts and comments do not move a decision to another expression. Token
edits in the declaration invalidate the ID; neighboring expressions have separate
IDs and are never covered by one disposition. The saved original/replacement/path
snapshot must also agree with the current identity.

Resolution is explicit:

- `matched`: exactly one current candidate with the same complete identity.
- `stale`: an ID exists uniquely but its saved identity snapshot disagrees.
- `ambiguous`: more than one current candidate has that ID; none is chosen.
- `unmatched`: no current candidate. **This does not assert source changed.**
  New discovery roots, build scope, replay, disabled families, exclusions, or
  source edits can all omit anchors. Inspect the scope and source before migrating.

Every nonmatched entry requires review and hides nothing. Review changed code
and copy its new identity explicitly; do not mass-update IDs by line proximity.
Canonical fingerprints are conservative source anchors, not whole-program
semantic guarantees: imports, dependencies, other declarations and build settings
can alter behavior without changing an ID. Re-review such decisions as needed.

## Audit and bounded review tasks

JSON `triage` contains the entire resolved ledger (including filtered/unmatched
entries), counts by disposition, reviewed/hidden mutant counts, the filter flag,
and the unchanged whole-run gate policy. Disposition counts count ledger entries;
reviewed counts uniquely matched decisions other than questions across the run;
hidden counts only findings removed after the status filter. Text prints the
ledger, reasons/provenance/resolutions and reviewed/hidden totals. Aggregate run
counts, statuses, efficacy and gate exit codes remain unchanged in both formats.

`--group-review` adds `review_groups`, each with an explainable criterion and at
most ten underlying IDs. Criteria are exact parsed package path, source file,
enclosing owner and current status. Members are sorted by canonical ID and split
into deterministic chunks. Groups are heuristic review tasks, **not** claims of
a shared root cause, equivalent behavior or test impact. Line shifts preserve
groups; membership/status changes may repartition them. Missing/truncated owner
metadata or ambiguous IDs leave findings standalone. Every finding remains in normal `groups`
unless explicitly filtered; omit `--group-review` for the ungrouped view.

## Precedence, migration and persistence safety

Configuration exclusions, generated-file rules, build scope, enabled families,
exact replay and diff selection act **before** report triage. Triage cannot
restore excluded findings or override their execution status. An excluded anchor
is unmatched, not automatically stale. Prefer an exact report disposition with
justification over a broad file exclusion for a reviewed behavior. Migrating from
an exclusion means deliberately removing/narrowing it, rerunning, then reviewing
individual identities. Triage never writes exclusions.

Future source directives (#15) are not implemented here: there is **no new comment
suppression syntax**. Such directives would be upstream selection policy; report
triage must not override them. Migration must preserve human justification and
provenance and re-anchor current findings, never convert neighboring code into a
blanket waiver or silently replace a ledger disposition.

When using triage, input, triage output, replay input and report output must be distinct paths,
including symlink/hardlink aliases. Outputs use the existing adjacent temporary
file/atomic replacement mechanism; failed execution leaves previous files intact.
Report and ledger files are individually atomic, **not a multi-file transaction**:
if ledger publication fails after report publication, the report may already be
new, while the old ledger remains. File destinations reject symlinks/devices;
triage output requires a real file path, not stdout. No in-place ledger writes.
Configuration keys mirror flags under `run` (`triage-input`, `triage-output`,
`hide-reviewed`, `group-review`).
