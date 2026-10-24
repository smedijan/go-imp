# FAQ

## Isn't code coverage enough?

Coverage tells you which code your tests execute, not whether they check its behavior. A test can hit a return
statement, ignore its result, and still raise coverage.

Mutation testing asks a different question: if that behavior changed, would a test fail? Use coverage to find
unexecuted code and imp to find weaknesses in the tests that do execute it. Neither metric proves correctness.

## What does meta-testing mean here?

Testing the effectiveness of your tests. imp uses mutation testing to do that: it introduces small changes to Go
source code and checks whether the existing suite detects them.

The goal isn't to collect a score. It's to find weak assertions, missing cases, and code whose design makes
important behavior hard to test.

## How does imp help with coding agents?

An agent can generate lots of code and tests quickly, but passing tests may only confirm the assumptions it made
while writing the implementation. Surviving mutations give it a concrete set of behaviors to investigate instead of
an instruction to simply "add more tests."

Keep the requirement as the source of truth. Have the agent explain each gap, improve behavior-focused tests or
simplify code, then rerun the checks. A [git hook](quick-start.md#automate-with-git-hooks) makes the check automatic.

## Where does imp work best?

Small- to medium-sized Go modules with fast, repeatable tests are a particularly good fit. Services, libraries, and
business logic with clear inputs and outputs tend to give quick, useful feedback.

Use diff-scoped runs during development and broader runs when you have more time. imp works with your existing
Go tests, so you don't need to adopt a separate test framework.

## Where is it less effective?

Large modules and slow test suites can make full mutation runs expensive—sometimes hours. imp runs tests repeatedly,
once for each covered mutation it executes. Tests that depend on external services, shared state, or timing can also
make the feedback slower or noisier.

Start with `imp run --diff origin/main`. Keep tests deterministic and inspect the
[worker and timeout settings](usage/commands/run/workers.md) before increasing parallelism.
[Integration mode](usage/commands/run/index.md#integration-mode) runs the full suite for each mutation and is generally
slower than the default package-level testing.

## Does every surviving mutation mean a bad test?

No. A surviving mutation may reveal a missing assertion or edge case, but it can also be *equivalent*: the changed
code has the same observable behavior for all valid inputs.

Inspect the mutation against the contract. Add a test when behavior matters, simplify redundant code when appropriate,
and document justified exclusions. Don't add tests of implementation details just to kill a mutant.

## Can mutations point to bad code as well as bad tests?

Yes. Difficult-to-test behavior can signal unclear responsibilities, tightly coupled code, or a contract that needs
clarifying. A mutation in redundant code may suggest removing that code rather than adding another test.

These are design clues, not automatic diagnoses. Review the code and requirement together.

## Why aren't uncovered mutations executed?

If no test reaches a mutation, running that suite against it won't tell you whether an assertion protects it.
imp reports these candidates as `NOT COVERED` and spends execution time on covered mutations instead.

## Should I aim for 100%?

Aim for confidence in important behavior. Equivalent mutations and low-value cases can make a perfect score costly
without making the software better. Review survivors, set pragmatic thresholds, and use the results alongside code
review, coverage, and other tests.

## Which version should I use?

Use the latest imp release. These docs track the latest imp and aren't versioned.
See [Install](install.md) for mise and Go options.

imp is still pre-1.0, so flags and configuration may change. Review release notes when upgrading; older release lines
aren't the target of this documentation.
