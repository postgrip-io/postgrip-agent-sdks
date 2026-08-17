# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

The wire-shape **source of truth** for the PostGrip Agent runtime. Two files of Go (`types.go` + `signing.go`) at this package root are consumed by the runtime and Go SDK, and **mirrored by hand** in `../typescript/src/types.ts` and `../python/src/postgrip_agent/types.py`. Any wire-format change must update all mirrors in the same monorepo commit; `tools/check_drift.py --monorepo` enforces matching field names.

The module path is `github.com/postgrip-io/postgrip-agent-sdks/protocol`.
Protocol releases use repository-prefixed tags such as `protocol/v0.3.0`.

**This package is not customer-facing.** End-users of the SDK never write `protocol.Task` directly — the Go SDK aliases the wire types in `../go/client/aliases.go` so customers stay within the SDK namespace. The only consumer that normally needs a protocol release is the Go SDK or server runtime.

## Commands

```sh
go test ./...                                  # types + signing tests
go test -run TestSign -v                       # single test or pattern
go vet ./...
gofmt -l . | grep -v '^tools/'                 # CI ignores tools/ (Python lives there)

python3 tools/check_drift.py --monorepo        # check every local SDK mirror
python3 tools/check_drift.py --from-github     # check vs TS/Python main on GitHub
python3 tools/check_drift.py --self-test       # exercise the detectors themselves
```

Root CI runs `gofmt -l`, `go vet`, `go test`, and the local monorepo drift check (`../.github/workflows/ci.yml`). **A failing drift job is a real signal**, not noise — it means the same commit contains incompatible wire definitions.

The drift job runs `--self-test` **before** the tree scan. A clean tree only proves today's type files agree; it never proves the checker still detects disagreement. The self-test caught a live bug in its own first run — the literal-const regex captured `const` as the name on single-line declarations — which is exactly the failure mode it exists to prevent.

## Architecture

### What lives in this package vs. what doesn't

`types.go` carries only structs that flow over the runtime-service wire — task envelopes, workflow execution rows, schedule shapes, agent enrollment requests, failure info, history events. SDK-side conveniences (e.g. the typed `ShellExecInput` / `WorkflowStartOptions` shapes) live in the SDK package and are **separate from** the wire types. Don't pull SDK ergonomics into protocol; don't add agent-implementation specifics here.

`signing.go` carries Ed25519 task-result signing — both the customer-side signer and the orchestrator-side verifier. It's wire-protocol concern (the canonical-request format is part of the contract) so it lives here, not in the SDK.

### The drift contract

The contract being checked is narrow: every exported Go struct that represents a wire shape has an equivalent type with matching name in `types.ts` and `types.py`, and every JSON-tagged field on that struct appears as a same-name field on the TS interface and the Python TypedDict. **Field types are not yet checked** (e.g. int vs string drift goes undetected); same for optional-vs-required. Both are tagged `# v2` in `tools/check_drift.py` and need a cross-language type table to address.

When you add, rename, or remove a field on a wire struct, update the TypeScript and Python paths in the same commit. The monorepo removes cross-repository branch and push-order races, but requiredness and field types still require human review.

### agent-sdk-go is checked differently

The Go SDK at `../go` is not a mirror — it imports this package and re-exports the wire types as aliases, so there are no field sets to compare. Its failure mode is *redeclaration*: when its protocol pin predates a type, it grows a local copy that compiles cleanly and mirrors nothing. Monorepo drift validation scans the SDK tree and fails on any struct or string-literal constant whose name collides with something protocol owns, matched case-insensitively. The fix is always the same: alias it.

This is not hypothetical — it is how `WorkflowRuntimePayload` and `TaskTypeWorkflowRuntime` came to exist twice in `agent-sdk-go/client/`, against a pin from before this package had either.

### Custom `UnmarshalJSON` on `Task`

`types.go` has a hand-rolled `Task.UnmarshalJSON` that re-decodes through a private `rawTask` shadow type. Reason: certain timestamp fields the runtime emits as either RFC3339 or epoch-seconds depending on origin; the shadow lets us normalize without affecting `Task`'s public field shape. **Don't simplify back to a default unmarshal** without verifying the runtime emits canonical RFC3339 in every code path — the shadow tolerates legacy formats that older runtime versions still produce.

### Signing format invariants

`signing.go` builds a canonical request string with a domain separator (`agent-task-v1\n`) followed by `METHOD\nPATH\nQUERY\nTIMESTAMP\nBODY_SHA256_BASE64\n`. **Don't change this format without bumping the version prefix** (`agent-task-v2`, etc.) — agents and orchestrators must agree on the bytes exactly, and an unannounced format change silently breaks every signed request. The version prefix exists precisely so future formats can coexist during a rollout.

The orchestrator-side verifier rejects timestamps outside `MaxAgentSignatureSkew` (5 minutes default) of server time. Symmetric window means the longest a captured signed request can stay replayable is ~10 minutes (past + future skew). Customer agent hosts must run NTP; clock-drifted agents show up in audits as `signature timestamp outside accepted skew`.

The signature header carries a key ID (`first 16 hex chars of sha256(pubkey)`) but the orchestrator looks up the active key by **agent identity**, not by key id, today. The header is preserved for future overlapping-key rotation. Don't introduce code that authenticates on the key id alone — agent identity is the trust root.

## Polyglot mirror

This is one of four packages in the `postgrip-agent-sdks` monorepo:

- `protocol/` (this) — the source of truth.
- `go/` — Go SDK; imports this package directly.
- `typescript/` — TS SDK; mirrors types in `src/types.ts`.
- `python/` — Python SDK; mirrors types in `src/postgrip_agent/types.py`.

The runtime (`postgrip-io/postgrip-web`) also imports this package directly and is the canonical implementer of the orchestrator side. Wire-shape changes that touch `types.go` should be reviewed with both the runtime and the SDKs in mind; a wire change without runtime support is wasted work.
