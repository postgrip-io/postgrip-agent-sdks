# test/

Vitest regression coverage for the SDK runtime, OpenAPI bindings, sandbox
transport, and published ESM module specifiers.

The companion `tests/postgrip-agent/` directory in `postgrip-web`
exercises the runtime contract from the server side. Tests here keep
SDK-specific behavior and packaging checks with the package they protect.
