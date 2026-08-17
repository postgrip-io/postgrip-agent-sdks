# test/

Reserved for future black-box / cross-language drift tests.

Go's package tests live next to their source at the module root because
Go's test toolchain requires `*_test.go` files to be colocated with the
package they exercise (`go test ./...`).
