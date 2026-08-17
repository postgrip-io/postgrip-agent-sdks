# Monorepo Migration

This repository is now the development source of truth for:

- `protocol/` from `postgrip-io/agent-sdk-protocol`
- `go/` from `postgrip-io/agent-sdk-go`
- `typescript/` from `postgrip-io/agent-sdk-typescript`
- `python/` from `postgrip-io/agent-sdk-python`

The imports preserve the original commit graphs through subtree merge commits.
The legacy repositories should remain available until their compatibility and
release responsibilities have been cut over.

## Releases

TypeScript and Python publish from this repository using `typescript/vX.Y.Z`
and `python/vX.Y.Z` tags. Before the first release, update the npm and PyPI
trusted-publisher configuration to repository `postgrip-agent-sdks` and the
new workflow filenames.

The monorepo is currently private. npm trusted publishing therefore runs
without provenance, and the existing public SDK repositories continue to host
customer-facing documentation. Keep mirroring documentation changes there.
Move Pages and public source links only after this repository is public and
Actions-based Pages has been enabled.

The existing Go module paths still resolve through the legacy repositories:

- `github.com/postgrip-io/agent-sdk-protocol`
- `go.postgrip.io/sdk`, whose vanity metadata points at `agent-sdk-go`

Until those paths are redirected or migrated, mirror release commits and tags
back to the corresponding legacy repository. Do not archive either Go
repository before `go get` succeeds through its public module path from a
clean module cache.

## Follow-up Cutover

After package publishing and Go resolution are verified:

1. Mark legacy repositories read-only and point their READMEs here.
2. Update external consumers, including the runtime, to the final protocol path.
3. Move issue templates, branch protections, environments, and repository secrets.
4. Archive legacy repositories only after released versions remain installable.

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
