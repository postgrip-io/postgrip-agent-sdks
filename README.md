# PostGrip Agent SDKs

The official Go, TypeScript, and Python SDKs for the PostGrip Agent runtime,
plus the shared Go wire-protocol package.

| Directory | Package | Purpose |
| --- | --- | --- |
| [`protocol/`](protocol/) | `github.com/postgrip-io/agent-sdk-protocol` | Wire types and request signing |
| [`go/`](go/) | `go.postgrip.io/sdk` | Go client and worker SDK |
| [`typescript/`](typescript/) | `@postgrip/agent` | TypeScript client and agent SDK |
| [`python/`](python/) | `postgrip-agent` | Python client and agent SDK |

## OpenAPI Contract

[`openapi.json`](openapi.json) is synchronized from the canonical server
contract in `postgrip-web/api/agent-openapi.json`. It generates the internal
method, path, authentication-lane, signing, streaming, and WebSocket metadata
used by all three SDK transports. Public SDK APIs and runtime-specific logic
remain hand-written.

```sh
python3 scripts/generate_openapi.py
python3 scripts/generate_openapi.py --check
python3 scripts/generate_openapi.py --check-source ../postgrip-web/api/agent-openapi.json
```

Never edit `*.gen.go` or a `generated/openapi.*` file directly. Update the
canonical contract, synchronize `openapi.json`, and rerun the generator.

## Development

Each package keeps its native toolchain. From the repository root:

```sh
go test ./protocol/... ./go/...
(cd typescript && bun install --frozen-lockfile && bun run typecheck && bun run test)
(cd python && PYTHONPATH=src python -m unittest discover -s test)
python3 protocol/tools/check_drift.py --self-test
python3 protocol/tools/check_drift.py --monorepo
python3 scripts/generate_openapi.py --check
```

The root `go.work` makes the Go SDK consume the local protocol module during
development. Cross-language wire changes must update `protocol/`,
`typescript/src/types.ts`, and `python/src/postgrip_agent/types.py` in one
commit.

See [MIGRATION.md](MIGRATION.md) for legacy Go module compatibility and release
cutover notes.
