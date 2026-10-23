# Welcome to imp

imp is a mutation-testing tool for Go that helps developers and coding agents write more testable code and stronger
tests. It changes code in small, realistic ways, then checks whether your tests catch those changes. Use the results to
find weak assertions, missing cases, and code that is difficult to test.

imp works especially well on small- to medium-sized Go modules, such as microservices, and can run locally or as a CI
quality gate.

As of now, imp doesn't work very well on very big Go modules, mainly because a run can take hours to complete.

[//]: # (@formatter:off)
!!! warning
    imp is still in its 0.x.x release, which, as per SemVer, doesn't guarantee backward compatibility. What this
    means for imp is that configuration flags and/or configuration files can change among minor releases. This may
    be important if you use imp in automated builds, for example in _continuous integration_ pipelines.

    Also, while in 0.x.x releases, only the current minor release will be maintained; if we are in v0.2.0, no patch
    releases will be provided for the v0.1.0 line.
    Once imp will reach it's 1.x.x release, it will start to be backward compatible, and we'll decide how many
    releases to maintain at the same time (ex. up to two minors before, as Go itself does).

[//]: # (@formatter:on)

## What is Mutation Testing

Code coverage is unreliable as a measure of test quality. It is too easy to have tests that exercise a piece of code but
don't test anything at all. Mutation testing works by mutating the code exercised by the tests and verifying if the
mutation is caught by the test suite. A passing test suite alone doesn't tell you whether tests would detect a
regression; imp helps answer that question.

## Features

- Discovers mutant candidates and tests them
- Only tests mutants covered by tests
- Can test mutants only in PR changes
- Supports five mutant types
- Yaml-based configuration
- Can run as quality gate on CI
