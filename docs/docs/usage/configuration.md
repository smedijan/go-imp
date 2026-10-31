# Configuration

imp can be configured via (in order of precedence):

1. command flags
2. environment variables
3. configuration files

## Command flags

Flags have the higher priority and override all the other means of setting behaviours. Please refer to the specific
command documentation to learn how to use them.

## Configuration files

imp can be configured with a configuration file.

### Location

The configuration file can be placed in (in order of precedence)

1. `./.imp.yaml` (the current directory)
2. The module root
3. `/etc/imp/.imp.yaml`
4. `$XDG_CONFIG_HOME/imp/.imp.yaml`
5. `$HOME/.imp.yaml`

[//]: # (@formatter:off)
!!! hint
    `XDG_CONFIG_HOME` is usually `~/.config`.

[//]: # (@formatter:on)

### Override

The config file can be overridden with the `--config` flag.

```shell
imp run --config=myConfig.yaml
```

### Reference

Here is a complete configuration file with all the properties set to their defaults:

```yaml
quiet: false
no-color: false
run:
  incremental: false
  incremental-refresh: false
  incremental-ttl-hours: 24
  incremental-max-entries: 2000
  integration: false
  dry-run: false
  tags: ""
  output: ""
  json: false
  verbose: false
  statuses: ""
  diff: ""
  diff-scope: changed-line
  workers: 0 #(1)
  test-cpu: 0 #(2)
  timeout-coefficient: 0 #(3)
  threshold: #(4)
    efficacy: 0
    mutant-coverage: 0
  exclude-files: [] #(5)
  include-files: [] #(6)
  exclude-workdir: [] #(7)

mutants:
  arithmetic-base:
    enabled: true
  conditionals-boundary:
    enabled: true
  conditionals-negation:
    enabled: true
  boolean-literal:
    enabled: false
  boolean-return:
    enabled: false
  error-return:
    enabled: false
  increment-decrement:
    enabled: true
  invert-assignments:
    enabled: false
  invert-bitwise:
    enabled: false
  invert-bwassign:
    enabled: false
  invert-negatives:
    enabled: true
  invert-logical:
    enabled: false
  invert-loopctrl:
    enabled: false
  remove-self-assignments:
    enabled: false

```

1. By default `0`, which means that imp will use the system CPUs number.
2. By default `0`, which means that no test process CPU will be enforced.
3. By default `0`, which means a default coefficient of `5` will be enforced.
4. Thresholds are set by default to `0`, which means they are not enforced.
5. Excluded files are set by default to empty list; generated Go files are still excluded automatically.
6. Include patterns override generated-file detection; explicit exclusions take precedence.
7. Worker-copy exclusions are empty by default. Set module-relative file or directory paths to omit them from worker
   copies; directories are pruned recursively. This is independent of `.gitignore`, which does not affect copies.
   Excluded assets may be required by builds or tests.

For further information check the specific command documentation.

[//]: # (@formatter:off)
!!! tip
    You can validate the configuration file using the provided JSON Schema (ex. using it in your editor). The schema
    can be found at [the configuration schema](../schema/configuration.json).

[//]: # (@formatter:on)

## Reporting

Grouped reporting is always enabled. Format, destination and status filtering are independent:

```yaml
quiet: true
run:
  json: true
  output: survivors.json
  statuses: lived,not-covered
```

This writes filtered JSON to `survivors.json` with no report on stdout. Set `json: false`
for human text; filenames do not choose the format. Empty output or `-` means stdout.
`statuses` is a comma-separated string; empty selects all statuses. Counts and quality
gates still describe the whole run. `quiet` suppresses only progress, never the report
or errors. `verbose` expands human source context for all selected statuses.

Equivalent environment variables include `IMP_RUN_JSON=true`,
`IMP_RUN_OUTPUT=survivors.json`, `IMP_RUN_STATUSES=lived,not-covered`, `IMP_QUIET=true`
and `IMP_NO_COLOR=true`. Explicit command flags override environment and configuration,
including `--json=false` and `--statuses=`.

See [reporting](commands/run/index.md#reporting) for the output contract.

## Environment variables

imp can be configured via environment variables as well. You can construct the variable name referring to the
configuration file format. They start with `IMP_`, and each dot and dash becomes an underscore.

For example:

```yaml
mutants:
  arithmetic-base:
    enabled: true
```

Can be set with:

```shell
export IMP_MUTANTS_ARITHMETIC_BASE_ENABLED=true
```