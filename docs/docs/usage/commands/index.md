---
title: The imp command
---

# imp

The `imp` command works with command and flags. Think of commands as verbs and flags as adjectives.

```shell
imp <command> [flags]
```

If you type

```shell
imp
```

a short usage summary will be printed.

At any time, you can get further help writing

```shell
imp help <command>
```

## Global flags

Global flags are not command specific.

### Config

:material-flag:`--config` · :material-sign-direction: Default: empty

Overrides the configuration file.

```shell
imp <command> --config=config.yml
```

### Quiet

:material-flag: `--quiet` / `-q` · :material-sign-direction: Default: false

Suppresses interactive progress on stderr, not the report or errors. To keep CI logs
quiet while retaining results, select JSON and a file destination:

```shell
imp run --quiet --json --output results.json
```

### No color

:material-flag: `--no-color` · :material-sign-direction: Default: false

Disables ANSI colors. Color is also disabled for redirected output, when `NO_COLOR`
is set, or when `TERM=dumb`. JSON is never colored.

### Version and help

`imp --version` prints the version. Use `imp run --help` for examples and flags grouped
by purpose. Reporting verbosity uses `-v` / `--verbose`, not version.

The final report goes to stdout unless `--output FILE` selects a file. Diagnostics
and interactive progress always go to stderr. See [reporting](run/index.md#reporting).
