# Quick start

[Install imp](install.md), then run it from your Go module root:

```sh
imp run --output-statuses lc
```

imp first runs your tests with coverage and stops if they fail. It then mutates covered code and reruns the tests.
`--output-statuses lc` shows `LIVED` mutations your tests missed and `NOT COVERED` mutations your tests never reached.
Look for missing assertions, boundary cases, or redundant code.
Some survivors are [equivalent mutations](faq.md#does-every-surviving-mutation-mean-a-bad-test), not test gaps.

## Check only your changes

```sh
git fetch origin main
imp run --diff origin/main --output-statuses lc
```

imp limits mutations to the diff against the merge base with `origin/main`; the baseline test run is unchanged.

## Automate with git hooks

Use [hk](https://hk.jdx.dev/) to check changed code before each push. Add this hook to your `hk.pkl`
(or use this as a minimal config):

```pkl
amends "package://github.com/jdx/hk/releases/download/v2.5.0/hk@2.5.0#/Config.pkl"

hooks {
    ["pre-push"] {
        steps {
            ["imp"] {
                glob = "**/*.go"
                check = "imp run --diff origin/main --output-statuses lc --threshold-efficacy 80"
            }
        }
    }
}
```

Install the hooks and make sure the comparison ref exists:

```sh
git fetch origin main
hk install
```

This example blocks a push when test efficacy on the diff falls below 80%. Adjust the threshold to your project.
The same imp command works in other hook runners or CI.

See [configuration](usage/configuration.md), the [command reference](usage/commands/run/index.md),
or [GitHub Actions](usage/ci/github-action.md) for more options.
