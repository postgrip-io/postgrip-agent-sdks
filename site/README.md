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

## Deploying to Cloudflare Pages

This is the smoothest free path; ~5 minutes total once you have a
Cloudflare account.

1. Go to **Cloudflare Pages → Create a project → Connect to Git** and pick
   the `postgrip-io/agent-sdk-go` repo.
2. Build settings:
   - **Framework preset**: None
   - **Build command**: *(empty)*
   - **Build output directory**: `site`
   - **Root directory**: *(empty / repo root)*
3. Deploy. You'll get a `*.pages.dev` URL. Verify it works:
   ```sh
   curl -s 'https://<project>.pages.dev/sdk?go-get=1' | grep go-import
   ```
   You should see the `<meta name="go-import" ...>` line.
4. **Add custom domain**: Pages project → Custom domains → Set up a
   custom domain → enter `go.postgrip.io`. Cloudflare will tell you
   exactly what DNS record to add (a `CNAME` from `go.postgrip.io` to
   `<project>.pages.dev`).
5. Add that one CNAME at your DNS registrar. Cloudflare auto-issues a
   TLS cert; usually live within minutes.

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
