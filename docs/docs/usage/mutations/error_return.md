---
title: Error returns
---

# Error returns <small>:material-sign-direction: opt-in</small>

`ERROR_RETURN` suppresses one explicit built-in `error` result by replacing its expression
with `nil`. It tests whether callers assert returned errors rather than ignore them.
Enable it with `--error-return=true` or `mutants.error-return.enabled: true`.
It is **disabled by default**, independently of Boolean return replacement.

```go
func Result(value int, err error) (int, error) { return value, err }
// candidate: return value, nil
```

Only result positions whose static type is built-in `error` (including aliases to it)
qualify. Custom error interfaces, concrete error implementations, arbitrary nilable values
and generic type parameters do not qualify. A concrete expression returned into a built-in
`error` result can qualify; it is the result position's type that matters.

Explicit tuples and explicit returns from functions with named results are supported.
Bare named returns and tuple-forwarding expressions are skipped. Already-nil expressions
(including parenthesized `nil`) are filtered. If `nil` is shadowed at the return site, the
candidate is skipped.

The replaced expression is **not evaluated**. Untouched result expressions preserve their
evaluation and side effects. Full candidate package type checking skips candidates that
would leave unused variables or imports. No initialization is removed or rewritten.
Type-invalid source packages are skipped for return analysis; infrastructure loading
failures abort the run. Invalid builds during execution remain `NOT VIABLE`, not `KILLED`.

See [Boolean returns](boolean_return.md) for shared selection/isolation behavior and the
[implementation plan and quality measurement](return-plan-and-measurement.md) for evidence.
