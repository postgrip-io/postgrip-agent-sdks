# Repository Guidelines

## Project Structure

This monorepo contains four independently released projects. `protocol/` is the Go wire-contract source of truth. `go/` contains the Go SDK, `typescript/` contains the Bun/TypeScript SDK, and `python/` contains the Python SDK. Tests remain inside each package using its native layout. Package-specific architecture notes live in each directory's `CLAUDE.md`.

## Development Commands

- `go test ./protocol/... ./go/...` runs both Go modules through the root workspace.
- `go vet ./protocol/... ./go/...` performs Go static analysis.
- `gofmt -w protocol go` formats Go sources.
- `cd typescript && bun install --frozen-lockfile && bun run typecheck && bun run build && bun run test` validates TypeScript.
- `cd python && python -m pip wheel --no-deps . -w /tmp/wheel && PYTHONPATH=src python -m unittest discover -s test` validates Python.
- `python3 protocol/tools/check_drift.py --self-test && python3 protocol/tools/check_drift.py --monorepo` verifies the cross-language contract.

## Style and Testing

Follow native conventions: `gofmt` and idiomatic exported comments for Go, two-space indentation and explicit public exports for TypeScript, and four-space PEP 8 formatting for Python. Name tests `TestBehavior` in Go, `*.test.ts` in TypeScript, and `test_*.py` in Python. Add regression coverage in every affected SDK when behavior is intended to match across languages.

## Wire Contract Changes

Treat JSON names, optionality, timestamp formats, and signing bytes as public API. HTTP models are OpenAPI-owned: update the canonical server contract, synchronize `openapi.json`, and regenerate every SDK. Runtime-only models remain protocol-owned and must update the TypeScript/Python mirrors atomically. The Go SDK must alias protocol types rather than redeclare them. Do not alter the `agent-task-v1` signing format without adding a version.

After synchronizing the contract, run `python3 scripts/generate_openapi.py`; CI runs it with `--check`. Never hand-edit generated operation, client, or wire-type files. Keep schemas closed and complete. Authentication, signing, long polling, archive streaming, and WebSocket framing stay in transport adapters.

## Commits and Pull Requests

Use concise Conventional Commit subjects such as `feat: add sandbox sessions`, `fix: preserve request context`, or `docs: clarify releases`. Keep commits scoped to one coherent cross-language change. PRs should identify affected packages, explain compatibility, link issues, and report the relevant tests plus the monorepo drift check. Package releases remain independent; use the package-prefixed tags documented in [MIGRATION.md](MIGRATION.md).
