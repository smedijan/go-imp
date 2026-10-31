---
title: Mutations
---

# About Mutations

Mutations are the core of imp' activity. Each mutation belongs to a group that defines its _flavour_. These
groups are called _mutation types_. imp supports various _mutation types_, each comprising one or more mutations.

When imp scans the source code under test, it looks for mutations and for each found mutation creates a _mutant_.
A _mutant_ is the "mutant" that actually changes the source code.

Each _mutant type_ can be enabled or disabled, and only a subset of mutations is enabled by default.

| MutationType                                           | Default |
|--------------------------------------------------------|:-------:|
| [ARITHMETIC BASE](arithmetic_base.md)                  |   YES   |
| [CONDITIONALS BOUNDARY](conditionals_boundary.md)      |   YES   |
| [CONDITIONALS NEGATION](conditionals_negation.md)      |   YES   |
| [BOOLEAN RETURN](boolean_return.md)                    |   NO    |
| [ERROR RETURN](error_return.md)                        |   NO    |
| [BOOLEAN LITERAL](boolean_literal.md)                  |   NO    |
| [INCREMENT DECREMENT](increment_decrement.md)          |   YES   |
| [INVERT NEGATIVES ](invert_negatives.md)               |   YES   |
| [INVERT LOGICAL ](invert_logical.md)                   |  FALSE  |
| [INVERT LOOP CTRL ](invert_loop.md)                    |  FALSE  |
| [INVERT ASSIGNMENTS ](invert_assignments.md)           |  FALSE  |
| [INVERT BITWISE ](invert_bitwise.md)                   |  FALSE  |
| [INVERT BWASSIGN ](invert_bitwise_assignments.md)      |  FALSE  |
| [REMOVE_SELF_ASSIGNMENTS ](remove_self_assignments.md) |  FALSE  |

## Unique edits and counts

After selecting enabled mutation types, imp keeps only one edit for the same source token location and replacement.
Identity token substitutions are discarded before formatting; formatting changes alone do not constitute a mutation.
This is mechanical deduplication, not general semantic-equivalence detection.

Global totals, status counts, and efficacy/coverage metrics count unique mutants, not contributing mutation types.
Per-operator statistics and finding types attribute each unique edit exclusively to the first enabled family in the
engine's stable token mapping order, so operator counts sum to the global total. For subtraction this primary family is
`INVERT_NEGATIVES` when enabled, otherwise `ARITHMETIC_BASE`. Both families independently select unary and binary
subtraction-to-addition edits; enabling both does not execute or count those edits twice. Token-mutator discovery
metadata retains all enabled contributing families without changing the report schema or the mutator interface.
