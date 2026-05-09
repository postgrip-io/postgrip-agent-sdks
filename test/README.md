# test/

Reserved for future black-box / integration tests that exercise the SDK
against a live runtime service.

Go's package-internal tests live next to their source at the module
root — the Go test toolchain requires `*_test.go` files to be colocated
with the package they exercise (`go test ./...`).
