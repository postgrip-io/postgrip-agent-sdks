# Repository Guidelines

## Project Structure & Module Organization

This repository is the Go source of truth for the PostGrip Agent wire contract. Package files live at the module root: `types.go` defines wire shapes, `sandbox.go` defines sandbox-related protocol behavior, and `signing.go` implements Ed25519 request signing. Tests are colocated as `*_test.go`. `tools/check_drift.py` verifies the hand-maintained TypeScript and Python mirrors. `doc/` and `test/` are reserved for future long-form documentation and cross-language black-box tests; read their local READMEs before adding files.

## Build, Test, and Development Commands

- `go test ./...` runs all package tests.
- `go test -run TestSign -v` runs matching tests while developing a focused change.
- `go vet ./...` performs Go static analysis.
- `gofmt -w *.go` formats root Go sources; CI rejects unformatted Go files.
- `python3 tools/check_drift.py --self-test` validates the drift detector itself.
- `python3 tools/check_drift.py --from-github` compares Go wire fields with the current TypeScript and Python mirrors.

The module targets Go 1.25. CI runs formatting, vet, tests, the drift self-test, and the GitHub-backed drift check.

## Coding Style & Naming Conventions

Follow standard `gofmt` output and idiomatic Go naming: exported identifiers use `PascalCase`, internal helpers use `camelCase`, and constants retain descriptive protocol prefixes such as `TaskTypeWorkflowRuntime`. Keep package declarations at `protocol`. Preserve existing JSON tag spelling exactly; tags are part of the external wire contract. Add concise comments for exported API and document protocol invariants near their implementation.

## Testing Guidelines

Use Go's `testing` package and name tests `TestBehavior`, in a colocated `*_test.go` file. Cover successful serialization/signing behavior and rejection or compatibility cases. There is no stated coverage threshold, but every wire-shape or signing change should include a regression test. Run the full CI-equivalent command set before opening a PR.

## Wire Contract Changes

Changes to JSON-tagged structs must be mirrored in `agent-sdk-typescript/src/types.ts` and `agent-sdk-python/src/postgrip_agent/types.py`. Use the same branch name across repositories, push all coordinated branches before opening PRs, and treat drift failures as contract errors. Do not change the signing canonical format without introducing a new version prefix.

## Commit & Pull Request Guidelines

Recent history favors concise, imperative Conventional Commit subjects such as `feat: add ...`, `fix: address ...`, and `docs: note ...`. Keep each commit scoped. PRs should explain wire compatibility, list coordinated SDK/runtime changes, link relevant issues, and report test and drift-check results. Screenshots are unnecessary unless documentation introduces visual output.
