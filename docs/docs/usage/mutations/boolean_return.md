---
title: Boolean returns
---

# Boolean returns <small>:material-sign-direction: opt-in</small>

`BOOLEAN_RETURN` replaces one explicit result expression with `true` or `false`, independently.
It tests whether callers assert returned flags rather than merely execute the function.
Enable it with `--boolean-return=true` or `mutants.boolean-return.enabled: true`.
It is **disabled by default**.

```go
func Allowed(flag bool) bool { return flag } // two candidates: true and false
```

Only result positions of built-in `bool` type (including aliases to it) qualify.
Defined Boolean types and generic type parameters are not included.
Explicit tuples such as `return value, flag, err` and explicit returns from functions
with named results are supported. Bare named returns (`return`) and tuple forwarding
(`return pair()`) are skipped. Nested function literals use their own result signatures.

Matching Boolean constants are filtered, including parenthesized constants and constant
expressions: `return true` has only the `false` candidate. Shadowed `true` or `false`
replacement names are skipped rather than resolved to user declarations.

The replaced expression is **not evaluated**. Untouched result expressions retain their
source, evaluation order and side effects. Candidates that introduce unused local variables
or imports are skipped by checking the complete candidate package; initializers are never
removed or rewritten to make a candidate compile. This is deliberately conservative.

This operator is independent of [Boolean literals](boolean_literal.md). Enabling both can
produce overlapping mutants at `return true` / `return false`; there is no cross-operator
deduplication. Source positions, snippets, coverage, diff selection and worker rollback use
the same reporting/execution pipeline as other operators.

See the [implementation plan and quality measurement](return-plan-and-measurement.md)
for reproducible acceptance counts and the rationale for retaining opt-in defaults.
