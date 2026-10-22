# Docker

Build the `imp` binary and Docker image from this repository; published container images are not available yet.

## As a pipeline step

In the _continuous integration_ tool of your choice, you can execute a step using the following syntax:

```shell
go build -o imp ./cmd/imp
docker build -t imp:local .
docker run --rm -v "$(pwd)":/app -w /app imp:local run .
```

The exact way to specify a runner step in the pipeline script depends on the tool of choice.

## As a stage in the Dockerfile

imp can be also run as a stage in the `Dockerfile`.

```dockerfile
FROM imp:local AS mutation-testing
WORKDIR /my/project/source
RUN imp run
```

For further details, please refer to
the [Docker multi stage builds documentation](https://docs.docker.com/develop/develop-images/multistage-build/).