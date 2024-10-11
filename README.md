# Centralized Sequencer

centralized-sequencer is an implementation of the [Generic Sequencer interface](https://github.com/rollkit/go-sequencing)
for modular blockchains. It runs a gRPC service,
which can be used by rollup clients to sequence transactions to Celestia da.

<!-- markdownlint-disable MD013 -->
[![build-and-test](https://github.com/rollkit/centralized-sequencer/actions/workflows/ci_release.yml/badge.svg)](https://github.com/rollkit/centralized-sequencer/actions/workflows/ci_release.yml)
[![golangci-lint](https://github.com/rollkit/centralized-sequencer/actions/workflows/lint.yml/badge.svg)](https://github.com/rollkit/centralized-sequencer/actions/workflows/lint.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/rollkit/centralized-sequencer)](https://goreportcard.com/report/github.com/rollkit/centralized-sequencer)
[![codecov](https://codecov.io/gh/rollkit/centralized-sequencer/branch/main/graph/badge.svg?token=CWGA4RLDS9)](https://codecov.io/gh/rollkit/centralized-sequencer)
[![GoDoc](https://godoc.org/github.com/rollkit/centralized-sequencer?status.svg)](https://godoc.org/github.com/rollkit/centralized-sequencer)
<!-- markdownlint-enable MD013 -->

## Minimum requirements

| Requirement | Notes          |
| ----------- |----------------|
| Go version  | 1.22 or higher |

## Installation

```sh
git clone https://github.com/rollkit/centralized-sequencer.git
cd centralized-sequencer
make build
./build/centralized-sequencer -h
```

## Usage

centralized-sequencer exposes a gRPC service that can be used with any gRPC
client to sequence rollup transactions to the celestia network.

## Example

Run centralized-sequencer by specifying DA network details:

<!-- markdownlint-disable MD013 -->
```sh
    ./build/centralized-sequencer -da_address <da_address> -da_auth_token <da_auth_token> -da_namespace $(openssl rand -hex 10)
```
<!-- markdownlint-enable MD013 -->

## Flags

<!-- markdownlint-disable MD013 -->
| Flag                         | Usage                                   | Default                     |
| ---------------------------- |-----------------------------------------|-----------------------------|
| `batch-time`            | time in seconds to wait before generating a new batch | 2 seconds |
| `da_address`              | DA address | `http:////localhost:26658`|
| `da_auth_token`               | auth token for the DA | `""` |
| `da_namespace`              | DA namespace where the sequencer submits transactions | `""` |
| `host`                | centralized sequencer host            | localhost |
| `port`             | centralized sequencer port | 50051 |
| `listen-all` |listen on all network interfaces (0.0.0.0) instead of just localhost|disabled|
| `metrics` |enable prometheus metrics|disabled|
| `metrics-address` |address to expose prometheus metrics|`":8080"`|
<!-- markdownlint-enable MD013 -->

See `./build/centralized-sequencer --help` for details.

### Tools

1. Install [golangci-lint](https://golangci-lint.run/welcome/install/)
1. Install [markdownlint](https://github.com/DavidAnson/markdownlint)
1. Install [hadolint](https://github.com/hadolint/hadolint)
1. Install [yamllint](https://yamllint.readthedocs.io/en/stable/quickstart.html)

## Helpful commands

```sh
# Print centralized-sequencer commands
centralized-sequencer --help

# Run unit tests
make test-unit

# Run all tests including integration tests
make test

# Run linters (requires golangci-lint, markdownlint, hadolint, and yamllint)
make lint
```

## Contributing

We welcome your contributions! Everyone is welcome to contribute, whether it's
in the form of code, documentation, bug reports, feature
requests, or anything else.

If you're looking for issues to work on, try looking at the [good first issue
list](https://github.com/rollkit/centralized-sequencer/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).
Issues with this tag are suitable for a new external contributor and is a great
way to find something you can help with!

Please join our
[Community Discord](https://discord.com/invite/YsnTPcSfWQ)
to ask questions, discuss your ideas, and connect with other contributors.

## Code of Conduct

See our Code of Conduct [here](https://docs.celestia.org/community/coc).
