# Contributing to imp

We welcome contributions that solve a clear problem and meet the project's quality standards.

## Discuss substantial changes first

Before starting a feature, behavioral change, or major refactor, open an
[issue](https://github.com/smedijan/go-imp/issues) describing the problem and proposed approach. Wait for discussion
before opening a PR. Small fixes can be submitted directly; when unsure, ask first.

## Pull requests

- You may have at most **two open PRs** at a time.
- Keep each PR focused. Explain the change and link the related issue.
- Add or update tests and documentation as needed.
- Run `hk check`; use `hk check --all` for all configured checks. `mise run all` also builds the binary.
- Address review feedback and keep your PR current.

PRs submitted without meaningful review, understanding, or validation will be closed. We encourage AI tools, and
expect contributors to use them thoughtfully, understand and verify their output, and deliver high-quality changes.
Substantial work submitted without prior discussion may also be closed.

## Local checks and push gates

Install the pinned tools and hooks in each checkout:

```shell
mise install
hk install --mise
```

The hook configuration alone does not install Git hooks. `hk check` runs the fast
checks; pre-push also runs security, vulnerability and mutation checks. Mutation testing
is triggered only by changed Go files and runs against the merge-base with `origin/main`,
using the efficacy and coverage thresholds in `.imp.yaml`. Only lived and uncovered
findings are printed; filtering does not weaken the quality gates.

Keep the comparison ref current and reproduce the push checks locally with:

```shell
git fetch origin main
hk run pre-push --from-ref origin/main --to-ref HEAD
```

To run just the mutation gate:

```shell
hk run pre-push --from-ref origin/main --to-ref HEAD --step mutation-test
```

Do not skip a failed gate or lower its thresholds to publish a change. Improve the
implementation/tests and rerun the checks.

## Black-box tests

Write all Go tests in the external `<package>_test` package and exercise the same
exported interface as callers. For CLI behavior, use `cmd.Execute` or the built binary.
Assert observable results rather than private helpers or fields; do not export
implementation details or add test-only export bridges to bypass this rule.

The `testpackage` linter enforces this in local hooks and CI, including
`export_test.go`, `*_internal_test.go`, and tests of `main` packages. Run
`golangci-lint run ./...` to check the policy alongside the other Go linters.

## DCO sign-off required

Sign off **every commit** under the [Developer Certificate of Origin](https://developercertificate.org/)
with `git commit -s`. This certifies you have the right to submit the contribution under the project license.
Never sign off on someone else's behalf.

## Quality and community

Install pinned tools with `mise install`. Keep changes focused and idiomatic; include tests for new behavior.
If you cannot run a check, say why in the PR.

Report bugs and propose ideas in the [issue tracker](https://github.com/smedijan/go-imp/issues).
Follow the [Code of Conduct](https://github.com/smedijan/go-imp/blob/main/CODE_OF_CONDUCT.md).
