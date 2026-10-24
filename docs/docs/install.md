# Install

Install the latest imp with mise or Go.

=== "mise"

    Install and [activate mise](https://mise.jdx.dev/getting-started.html), then run:

    ```sh
    mise use -g github:smedijan/go-imp@latest
    ```

=== "Go"

    Go 1.25+:

    ```sh
    go install github.com/smedijan/go-imp/cmd/imp@latest
    ```

    Make sure `$(go env GOPATH)/bin` (or `GOBIN`, if set) is on your `PATH`.

Verify the installation:

```sh
imp --help
```

[Run your first meta-test :material-arrow-right:](quick-start.md){ .md-button .md-button--primary }
