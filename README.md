# imp

[![CI](https://github.com/smedijan/go-imp/actions/workflows/ci.yml/badge.svg)](https://github.com/smedijan/go-imp/actions/workflows/ci.yml)

<p align="center">
  <img src="docs/docs/assets/images/imp-logo.png" alt="imp — mutation testing for Go" width="320">
</p>

`imp` is meta-testing for Go: it tests whether your tests catch broken behavior. It introduces small, realistic
faults into your code and reports which ones your suite misses. Find weak assertions, missing edge cases, and
hard-to-test code—and keep coding agents focused on correctness, not just green checkmarks.

Coverage tells you which code ran. imp helps you find out whether your tests would notice if it were wrong.

## Install

Install the latest release with:

```sh
go install github.com/smedijan/go-imp/cmd/imp@latest
```

Then run it from a Go module:

```sh
imp run
```

See the [installation guide](docs/docs/install.md) for mise and Go options.
Then follow the [quick start](docs/docs/quick-start.md) or read the [FAQ](docs/docs/faq.md).

## Documentation

The [website](https://imp.smedijan.se/) has installation, usage, and configuration guides.

Documentation tracks the latest imp; there are no versioned docs. The site uses
[Zensical](https://zensical.org/), with the existing `docs/mkdocs.yml` configuration format.
The tasks resolve the latest available Zensical release:

```sh
mise run docs-serve  # Live preview at http://127.0.0.1:8000
mise run docs-build  # Strict production build
```

## License

The source code is licensed under [Apache-2.0](LICENSE). Documentation by the imp authors is licensed under
[CC BY-SA 4.0](http://creativecommons.org/licenses/by-sa/4.0/).

---

`imp` is an independent continuation of [go-gremlins/gremlins](https://github.com/go-gremlins/gremlins), helping
developers and coding agents build Go test suites that catch behavioral regressions.
