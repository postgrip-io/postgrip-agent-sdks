# Vanity import path

The Go module declares its path as `go.postgrip.io/sdk` (vanity), with the
upstream code on GitHub at `github.com/postgrip-io/agent-sdk-go`. For external
`go get` and pkg.go.dev to resolve the vanity path, `go.postgrip.io` must
serve a small static page with `<meta name="go-import">` and
`<meta name="go-source">` tags.

That page lives in [`site/`](../site/) — it's a deployment-ready
`index.html` plus a `_redirects` file for path rewrites. See
[`site/README.md`](../site/README.md) for the deployment walkthrough
(Cloudflare Pages + one CNAME record).

## Why this matters

Until `https://go.postgrip.io/sdk?go-get=1` returns the meta tags, two
things stay broken:

- `go get go.postgrip.io/sdk@<tag>` fails (proxy.golang.org can't resolve
  the path).
- `pkg.go.dev/go.postgrip.io/sdk` shows no module (it relies on the proxy).

Fetching via the GitHub URL directly (`go get github.com/postgrip-io/agent-sdk-go`)
*also* fails because the go.mod's declared module path doesn't match — Go
errors with "module declares its path as: go.postgrip.io/sdk; but was
required as: github.com/postgrip-io/agent-sdk-go".

The vanity page is the single piece that unblocks all of this.
