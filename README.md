# PostGrip Agent SDKs

The official Go, TypeScript, and Python SDKs for the PostGrip Agent runtime,
plus the shared Go wire-protocol package.

| Directory | Package | Purpose |
| --- | --- | --- |
| [`protocol/`](protocol/) | `github.com/postgrip-io/postgrip-agent-sdks/protocol` | Wire types and request signing |
| [`go/`](go/) | `go.postgrip.io/sdk` | Go client and worker SDK |
| [`typescript/`](typescript/) | `@postgrip/agent` | TypeScript client and agent SDK |
| [`python/`](python/) | `postgrip-agent` | Python client and agent SDK |

## OpenAPI Contract

[`openapi.json`](openapi.json) is synchronized from the canonical server
contract in `postgrip-web/api/agent-openapi.json`. It generates closed public
wire models, per-operation request/response types, and typed low-level clients
for Go, TypeScript, and Python. All 42 operations generate transport metadata;
the 40 ordinary JSON operations also generate client methods. Stable public
facades delegate to these clients. Workspace archive streaming and sandbox
WebSocket sessions retain custom adapters while consuming generated metadata.

```sh
python3 scripts/generate_openapi.py
python3 scripts/generate_openapi.py --check
python3 scripts/generate_openapi.py --check-source ../postgrip-web/api/agent-openapi.json
```

Never edit `*.gen.go`, `typescript/src/generated/openapi.ts`, or
`python/src/postgrip_agent/openapi.py` directly. Update the canonical contract,
synchronize `openapi.json`, and rerun the generator.

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
development. HTTP wire changes belong in the canonical server contract and
must be followed by synchronization and regeneration here. Runtime-only wire
changes still update `protocol/` and the TypeScript/Python mirrors together.

See [MIGRATION.md](MIGRATION.md) for the hard-cutover status and release tags.
