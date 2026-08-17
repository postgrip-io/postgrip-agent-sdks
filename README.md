# PostGrip Agent SDKs

The official Go, TypeScript, and Python SDKs for the PostGrip Agent runtime,
plus the shared Go wire-protocol package.

| Directory | Package | Purpose |
| --- | --- | --- |
| [`protocol/`](protocol/) | `github.com/postgrip-io/agent-sdk-protocol` | Wire types and request signing |
| [`go/`](go/) | `go.postgrip.io/sdk` | Go client and worker SDK |
| [`typescript/`](typescript/) | `@postgrip/agent` | TypeScript client and agent SDK |
| [`python/`](python/) | `postgrip-agent` | Python client and agent SDK |

## Development

Each package keeps its native toolchain. From the repository root:

```sh
go test ./protocol/... ./go/...
(cd typescript && bun install --frozen-lockfile && bun run typecheck && bun run test)
(cd python && PYTHONPATH=src python -m unittest discover -s test)
python3 protocol/tools/check_drift.py --self-test
python3 protocol/tools/check_drift.py --monorepo
```

The root `go.work` makes the Go SDK consume the local protocol module during
development. Cross-language wire changes must update `protocol/`,
`typescript/src/types.ts`, and `python/src/postgrip_agent/types.py` in one
commit.

See [MIGRATION.md](MIGRATION.md) for legacy Go module compatibility and release
cutover notes.
