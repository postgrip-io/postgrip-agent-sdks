// Package protocol carries the wire-format types and Ed25519 task-result
// signing logic for the PostGrip Agent runtime service. It is the single
// source of truth shared by the postgrip-web runtime, the customer-facing
// Go SDK, and (via generated or mirrored type definitions) the TypeScript and
// Python SDKs in this monorepo.
//
// # Import
//
//	import "github.com/postgrip-io/postgrip-agent-sdks/protocol"
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
// Go package files live at this module root. The TypeScript and Python SDKs
// keep their sources under their language directories in the same repository.
package protocol
