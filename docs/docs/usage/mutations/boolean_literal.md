---
title: Boolean literals
---

# Boolean literals <small>:material-sign-direction: opt-in</small>

The `BOOLEAN_LITERAL` operator changes a predeclared `true` expression to `false` and vice versa. It recognizes only
unshadowed identifiers used as values; declarations and selector names are not mutations. Lexical shadowing is resolved
by Go's parser and across files in the same package, so declarations named `true` or `false` are left alone.

Mutations are discovered in assignment and declaration values, returned values, call arguments, composite literal
values, conditions, case values, send values, and nested unary/binary/parenthesized expressions. Composite literal keys
are excluded because determining whether a key is a field name or a boolean map key requires type information. Other
identifier contexts are deliberately excluded. The replacement is another boolean constant, so it remains valid in each
supported value context.

This operator is disabled by default (`--boolean-literal=true` opts in). It is independent of conditional negation and
unary negation. A self-run with only this operator enabled found 82 candidates: 54 killed, 10 lived, 17 not covered,
and 1 not viable. It remains opt-in given the eight observed survivors and the added execution cost; counts vary by
source and test coverage.
