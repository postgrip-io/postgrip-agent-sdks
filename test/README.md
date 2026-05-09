# test/

Reserved for future black-box / integration tests that exercise the SDK
against a live runtime service.

Go's package-internal tests live next to their source under `src/` —
the Go test toolchain requires `*_test.go` files to be colocated with
the package they exercise (`go test ./src/...`).
