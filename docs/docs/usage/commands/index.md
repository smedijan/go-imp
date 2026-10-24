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

### Silent

:material-flag:`--silent`/`-s` · :material-sign-direction: Default: false

Makes imp work in _silent mode_, which means only errors will be reported on STDOUT. This is useful in CI runs
when you don't want to clutter the log, but just read the results from a file or check the exit error code in
combination with a threshold configuration.

!!! warning
    Note that imp will be completely silent if there aren't errors, it doesn't mean it is unresponsive.

```shell
imp <command> --silent
```
