# imp

[![CI](https://github.com/smedijan/go-imp/actions/workflows/ci.yml/badge.svg)](https://github.com/smedijan/go-imp/actions/workflows/ci.yml)

`imp` is a mutation-testing tool for Go. It mutates code exercised by your tests and reports which mutations your test suite catches.

## Install

Requires Go. Until the first `imp` release, install the main branch with:

```sh
go install github.com/smedijan/go-imp/cmd/imp@main
```

Then run it from a Go module:

```sh
imp unleash
```

See [the installation guide](docs/docs/install.md) and [usage documentation](docs/docs/index.md).

## Provenance and license

`imp` is an independent continuation of [go-gremlins/gremlins](https://github.com/go-gremlins/gremlins), with its Git history preserved. The source code is licensed under [Apache-2.0](LICENSE). Carried-over documentation remains under [CC BY-SA 4.0](http://creativecommons.org/licenses/by-sa/4.0/).
