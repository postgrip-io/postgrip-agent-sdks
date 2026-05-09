# site/

Static landing page that doubles as the Go vanity import resolver for
`go.postgrip.io/sdk`.

When `go get go.postgrip.io/sdk[/...]` runs, Go's module proxy fetches
`https://go.postgrip.io/sdk?go-get=1`, parses the `<meta name="go-import">`
tag in the response, and uses it to find the upstream git repo
(`github.com/postgrip-io/agent-sdk-go`). Without this page deployed at
`go.postgrip.io`, `go get` fails — and pkg.go.dev never populates.

## Files

- `index.html` — the page. Carries the `go-import` and `go-source` meta
  tags Go needs, plus a clean human-visible landing for browsers.
- `_redirects` — rewrites `/sdk` and `/sdk/*` to `/index.html` (200) so
  any sub-package path lands on the meta tags. Cloudflare Pages and
  Netlify both honor this format.

## Deploying to Cloudflare (Workers + Static Assets)

Cloudflare merged the classic "Pages" flow into the unified Workers
deploy UI. The `wrangler.toml` at the repo root tells the Workers
runtime to serve this `site/` directory as a static asset bundle —
no Worker code, no build step.

1. Cloudflare dashboard → **Workers & Pages → Create → Continue with GitHub**.
2. Pick the `postgrip-io/agent-sdk-go` repo.
3. Configure:
   - **Project name**: `agent-sdk-go` *(matches the CNAME target you'll add)*
   - **Production branch**: `main`
   - **Build command**: *(leave empty)*
   - **Deploy command**: `npx wrangler deploy` *(default; reads `wrangler.toml`)*
   - **Path**: `/`
   - **API token**: let Cloudflare create one automatically.
4. Click **Save and Deploy**. First deploy is ~30s.
5. The project gets a `*.workers.dev` (or `*.pages.dev` legacy) URL.
   Verify it serves the meta tag:
   ```sh
   curl -s 'https://<project-url>/sdk?go-get=1' | grep go-import
   ```
6. **Custom domain**: Project → **Settings → Domains & Routes → Add**
   → enter `go.postgrip.io`.
7. **DNS**: a single CNAME at your DNS registrar pointing
   `go.postgrip.io` → `<project>.workers.dev` (or whatever Cloudflare
   shows as the target). If your DNS zone is on Cloudflare too, the
   custom-domain wizard auto-creates the CNAME for you.
8. TLS cert auto-issues; usually live within minutes.

## Verifying everything works

After the custom domain is live:

```sh
# Vanity meta tag is served:
curl -s 'https://go.postgrip.io/sdk?go-get=1' | grep go-import

# go get resolves:
mkdir /tmp/test && cd /tmp/test && go mod init test
go get go.postgrip.io/sdk@latest

# pkg.go.dev populates after the first fetch (may take a few minutes):
open https://pkg.go.dev/go.postgrip.io/sdk
```

## Alternative hosts

The same `index.html` + `_redirects` (or equivalent rewrite config) work on:

- **Netlify** — same `_redirects` syntax.
- **GitHub Pages on a separate repo** — works, but rewrite rules are
  trickier (need a Jekyll plugin or per-path `index.html` symlinks).
- **S3 + CloudFront** — works with a CloudFront function or a default
  document setup.

Cloudflare Pages is recommended because it's free, supports `_redirects`
out of the box, and integrates cleanly with custom domains.

## Updating the page

Edit `site/index.html` and merge to `main`. Cloudflare Pages auto-deploys
on every push to the configured branch (default: `main`). The vanity meta
tags are static and shouldn't need to change unless the upstream repo
moves.
