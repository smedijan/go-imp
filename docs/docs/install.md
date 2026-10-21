# Install

`imp` is currently distributed with `go install`.

## Requirements

Install a supported version of the [Go toolchain](https://go.dev/dl/).

## Install the main branch

Until the first `imp` release is tagged, install the main branch:

```sh
go install github.com/smedijan/go-imp/cmd/imp@main
```

Make sure `$(go env GOPATH)/bin` (or `$(go env GOBIN)`, if set) is on your `PATH`. Verify the installation with:

```sh
imp --help
```

Homebrew packages and pre-built binary releases are not available yet.
