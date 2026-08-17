# Monorepo Migration

This repository is now the development source of truth for:

- `protocol/` from `postgrip-io/agent-sdk-protocol`
- `go/` from `postgrip-io/agent-sdk-go`
- `typescript/` from `postgrip-io/agent-sdk-typescript`
- `python/` from `postgrip-io/agent-sdk-python`

The imports preserve the original commit graphs through subtree merge commits.
The project is performing a hard cutover because it is still single-user and
pre-production. No legacy source mirror will remain writable.

## Hard-Cutover Sequence

1. Publish `protocol/v0.3.0` from the new protocol module path.
2. Move the Go SDK and server runtime imports to the monorepo paths.
3. Publish `go/v0.12.0`, `typescript/v0.12.0`, and `python/v0.12.0` here.
4. Verify clean installs, redirect the legacy repositories, and archive them.

The final Go module paths are:

- `github.com/postgrip-io/postgrip-agent-sdks/protocol`
- `github.com/postgrip-io/postgrip-agent-sdks/go`

TypeScript and Python retain their registry names and use package-prefixed
tags. The npm and PyPI trusted publishers must target this repository and the
`npm` and `pypi` GitHub environments respectively.

## OpenAPI Generation Cutover

The code-generation cutover is complete. The canonical contract defines every
public HTTP request, response, path, query, and enum shape. The generator emits
public models, operation aliases, metadata for all 42 operations, and typed
low-level clients for the 40 JSON operations in Go, TypeScript, and Python.
Existing ergonomic methods are compatibility adapters over generated clients,
so their public call patterns remain stable without owning duplicate schemas.

Two operations remain custom by design: gzipped workspace archive streaming
and sandbox WebSocket sessions. Session refresh, authentication, signing, and
long-poll behavior also stay in transport adapters, but their routes and wire
types come from generated metadata. CI rejects stale generated output and
checks OpenAPI-owned models against the Go protocol wire types.

## Landing This Migration

Merge the consolidation pull request with a merge commit. Do not squash or
rebase it: the four subtree imports preserve their original histories through
second-parent links, which those merge strategies would discard.
