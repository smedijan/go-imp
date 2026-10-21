# GitHub Actions

Install and run `imp` directly in your workflow:

```yaml
name: imp
on: [push, pull_request]
jobs:
  mutation-testing:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go install github.com/smedijan/go-imp/cmd/imp@main
      - run: imp unleash --silent
```
