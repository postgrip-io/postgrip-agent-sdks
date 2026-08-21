# PostGrip Agent SDKs

[![CI](https://github.com/postgrip-io/postgrip-agent-sdks/actions/workflows/ci.yml/badge.svg)](https://github.com/postgrip-io/postgrip-agent-sdks/actions/workflows/ci.yml)
[![Documentation](https://img.shields.io/badge/docs-online-2563EB?logo=github&logoColor=white)](https://postgrip-io.github.io/postgrip-agent-sdks/)
[![Go Reference](https://pkg.go.dev/badge/github.com/postgrip-io/postgrip-agent-sdks/go.svg)](https://pkg.go.dev/github.com/postgrip-io/postgrip-agent-sdks/go)
[![npm](https://img.shields.io/npm/v/%40postgrip%2Fagent.svg?logo=npm)](https://www.npmjs.com/package/@postgrip/agent)
[![PyPI](https://img.shields.io/pypi/v/postgrip-agent.svg?logo=pypi)](https://pypi.org/project/postgrip-agent/)

The official Go, TypeScript, and Python SDKs for submitting work to PostGrip
agents and building managed, durable workflow runtimes. The repository also
contains the shared Go wire-protocol module and the generated OpenAPI clients
used by all three SDKs.

> [!IMPORTANT]
> These SDKs connect applications and managed workflow runtimes to an existing
> PostGrip agent pool. They do not enroll or replace the host PostGrip agent.

## Choose an SDK

| SDK | Package | Documentation | Latest release |
| --- | --- | --- | --- |
| Go | `github.com/postgrip-io/postgrip-agent-sdks/go` | [Guide](https://postgrip-io.github.io/postgrip-agent-sdks/go/) · [API](https://pkg.go.dev/github.com/postgrip-io/postgrip-agent-sdks/go) | [![Go release](https://img.shields.io/github/v/release/postgrip-io/postgrip-agent-sdks?filter=go%2Fv*&label=go)](https://github.com/postgrip-io/postgrip-agent-sdks/releases?q=go%2Fv) |
| TypeScript | `@postgrip/agent` | [Guide](https://postgrip-io.github.io/postgrip-agent-sdks/typescript/) · [npm](https://www.npmjs.com/package/@postgrip/agent) | [![npm version](https://img.shields.io/npm/v/%40postgrip%2Fagent.svg)](https://www.npmjs.com/package/@postgrip/agent) |
| Python | `postgrip-agent` | [Guide](https://postgrip-io.github.io/postgrip-agent-sdks/python/) · [PyPI](https://pypi.org/project/postgrip-agent/) | [![PyPI version](https://img.shields.io/pypi/v/postgrip-agent.svg)](https://pypi.org/project/postgrip-agent/) |
| Protocol | `github.com/postgrip-io/postgrip-agent-sdks/protocol` | [Source](protocol/) | [![Protocol release](https://img.shields.io/github/v/release/postgrip-io/postgrip-agent-sdks?filter=protocol%2Fv*&label=protocol)](https://github.com/postgrip-io/postgrip-agent-sdks/releases?q=protocol%2Fv) |

Install the latest SDK for your language:

```sh
# Go
go get github.com/postgrip-io/postgrip-agent-sdks/go@latest

# TypeScript / JavaScript
npm install @postgrip/agent

# Python
pip install postgrip-agent
```

See the [Go](go/README.md), [TypeScript](typescript/README.md), or
[Python](python/README.md) README for a complete quick start and language-native
examples.

## What the SDKs provide

- Submit shell, container, and managed `workflow.runtime` tasks to PostGrip
  agent queues.
- Define durable workflows and activities with replay-safe timers, retries,
  child workflows, cancellation, and signals. TypeScript and Python also
  dispatch query and update handlers; Go handler dispatch remains on the
  roadmap.
- Create schedules and inspect workflow execution history.
- Upload workspaces and manage persistent sandboxes, including command and
  interactive session execution.
- Stream task events, milestones, standard output, and standard error.
- Share closed, typed request and response models generated from one OpenAPI
  contract across Go, TypeScript, and Python.

## Managed runtime model

1. An application client authenticates with PostGrip and submits a
   `workflow.runtime` task to an existing agent queue.
2. A host PostGrip agent leases the task and launches the requested command or
   image.
3. The host injects scoped runtime credentials into the managed process.
4. The SDK runtime registers workflows and activities, then polls and completes
   their task families using those delegated credentials.

This keeps agent enrollment and infrastructure ownership in the host agent
while application workflow code stays portable across the SDKs. Start with the
[Go](https://postgrip-io.github.io/postgrip-agent-sdks/go/quickstart),
[TypeScript](https://postgrip-io.github.io/postgrip-agent-sdks/typescript/quickstart/),
or [Python](https://postgrip-io.github.io/postgrip-agent-sdks/python/quickstart/)
quick start.

## Repository layout

| Directory | Purpose |
| --- | --- |
| [`protocol/`](protocol/) | Go wire types, request signing, and runtime-only contract source of truth |
| [`go/`](go/) | Go client and managed workflow runtime SDK |
| [`typescript/`](typescript/) | TypeScript client and managed workflow runtime SDK |
| [`python/`](python/) | Python client and managed workflow runtime SDK |
| [`openapi.json`](openapi.json) | Synchronized public HTTP contract used by every SDK generator |
| [`scripts/`](scripts/) | OpenAPI synchronization and SDK generation tooling |

Each project is an independently versioned and released package. Tests and
longer-form documentation remain inside the package that owns them.

## OpenAPI contract

[`openapi.json`](openapi.json) is synchronized from the canonical server
contract in `postgrip-web/api/agent-openapi.json`. It generates closed public
wire models, per-operation request and response types, transport metadata, and
typed low-level clients for Go, TypeScript, and Python. Stable public facades
delegate to those generated clients.

Workspace archive streaming, sandbox WebSocket sessions, authentication,
signing, and long-poll behavior remain in transport adapters while consuming
generated routes and wire types. Runtime-only models remain protocol-owned and
must be updated across the TypeScript and Python mirrors together.

```sh
python3 scripts/generate_openapi.py
python3 scripts/generate_openapi.py --check
python3 scripts/generate_openapi.py --check-source ../postgrip-web/api/agent-openapi.json
```

Never edit generated operation metadata, clients, or wire-type files directly.
This includes `go/client/openapi_*.gen.go`,
`typescript/src/generated/openapi.ts`,
`python/src/postgrip_agent/generated/`, and
`python/src/postgrip_agent/openapi.py`. Update the canonical contract,
synchronize `openapi.json`, and rerun the generator.

## Development

Run the relevant native toolchain from the repository root:

```sh
# Go modules
go test ./protocol/... ./go/...
go vet ./protocol/... ./go/...

# TypeScript
(cd typescript && bun install --frozen-lockfile && bun run typecheck && bun run build && bun run test)

# Python
(cd python && python -m pip wheel --no-deps . -w /tmp/wheel && PYTHONPATH=src python -m unittest discover -s test)

# Cross-language contract and generated-client checks
python3 protocol/tools/check_drift.py --self-test
python3 protocol/tools/check_drift.py --monorepo
python3 scripts/generate_openapi.py --check
```

The root `go.work` makes the Go SDK consume the local protocol module during
development. Add regression coverage in every affected SDK when behavior is
intended to match across languages.

## Releases

The four projects release independently from package-prefixed tags:

| Project | Tag format | Destination |
| --- | --- | --- |
| Protocol | `protocol/vX.Y.Z` | Go module proxy and GitHub Releases |
| Go SDK | `go/vX.Y.Z` | Go module proxy and GitHub Releases |
| TypeScript SDK | `typescript/vX.Y.Z` | npm and GitHub Releases |
| Python SDK | `python/vX.Y.Z` | PyPI and GitHub Releases |

Tag pushes run the package-specific validation and publishing workflows. npm
and PyPI publishing use trusted publishing from this repository; no long-lived
registry token is stored in the release workflows. Never move or reuse a
published tag—release a new version instead.

## Monorepo migration

This repository is the development and release source of truth. The former
`agent-sdk-go`, `agent-sdk-protocol`, `agent-sdk-python`, and
`agent-sdk-typescript` repositories are archived with redirect notices.
Package names on npm and PyPI remain unchanged; Go consumers must use the
monorepo module paths shown above.

See [MIGRATION.md](MIGRATION.md) for the completed hard-cutover sequence and
compatibility details.

## Support and license

Report SDK bugs and request features in the
[monorepo issue tracker](https://github.com/postgrip-io/postgrip-agent-sdks/issues).
The protocol and SDK source is licensed under the Apache License 2.0; see the
license files in [protocol](protocol/LICENSE), [Go](go/LICENSE),
[TypeScript](typescript/LICENSE), and [Python](python/LICENSE).
