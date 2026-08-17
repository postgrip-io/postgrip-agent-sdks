// Package protocol carries the wire-format types and Ed25519 task-result
// signing logic for the PostGrip Agent runtime service. It is the single
// source of truth shared by the postgrip-web runtime, the customer-facing
// agent-sdk-go, and (via hand-mirrored type definitions) agent-sdk-typescript
// and agent-sdk-python.
//
// # Import
//
//	import "github.com/postgrip-io/agent-sdk-protocol"
//
// # Stability
//
// Types in this package are the on-the-wire contract. Any change here
// lands simultaneously in the postgrip-web runtime and is implicitly
// contracted against the TS/Python SDK type mirrors. The drift guard at
// tools/check_drift.py fails CI when a Go type's exported field set does
// not match the TS/Python equivalents.
//
// # Layout
//
// Go package files live at the module root (idiomatic Go layout) so a
// default `go get` + `import` works without surprises. The TypeScript
// and Python SDK repos keep their sources under src/ per each language's
// own conventions; only the test/, doc/, and .github/ siblings are
// uniformly placed across all four repos.
package protocol
