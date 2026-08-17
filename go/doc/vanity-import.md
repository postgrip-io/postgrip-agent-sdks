# Vanity import path

The Go module declares its path as `go.postgrip.io/sdk` (vanity). Development
source lives in `postgrip-io/postgrip-agent-sdks/go`, while the vanity
metadata still resolves releases through the legacy
`github.com/postgrip-io/agent-sdk-go` distribution repository. For external
`go get` and pkg.go.dev to work, `go.postgrip.io` must serve a small static
page with `<meta name="go-import">` and `<meta name="go-source">` tags.

That page lives in [`site/`](../site/) — a deployment-ready `index.html`
served via Cloudflare Workers Assets (config in
[`wrangler.toml`](../wrangler.toml)). See
[`doc/site-deployment.md`](./site-deployment.md) for the deployment
walkthrough (one CNAME, one Cloudflare Workers project).

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

Do not repoint the vanity page directly at the monorepo without also solving
subdirectory module resolution and validating a clean-cache `go get`. See the
root `MIGRATION.md` for the compatibility cutover.
