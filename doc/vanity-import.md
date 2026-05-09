# Vanity import path setup

The Go module declares its path as `go.postgrip.io/sdk`. For external
`go get` to resolve that to this GitHub repo, the `go.postgrip.io` domain
must serve a small HTML page with the right meta tags. That setup lives
outside this repo and is described here so it doesn't get lost.

## What `go get` does

When a user runs `go get go.postgrip.io/sdk/client`, the Go toolchain:

1. Issues `GET https://go.postgrip.io/sdk?go-get=1`.
2. Parses the response for `<meta name="go-import">` and `<meta name="go-source">`.
3. Uses the `go-import` content to find the VCS (git) and clone URL.
4. Uses `go-source` (optional) to render correct godoc.org / pkg.go.dev links.

If those tags aren't there, `go get` fails with `unrecognized import path`.

## Required meta tags

Serve this exact HTML at `https://go.postgrip.io/sdk` (and ideally any
deeper path under `/sdk/...` — the redirect should handle prefix matches):

```html
<!doctype html>
<html>
<head>
<meta name="go-import" content="go.postgrip.io/sdk git https://github.com/postgrip-io/agent-sdk-go">
<meta name="go-source" content="go.postgrip.io/sdk https://github.com/postgrip-io/agent-sdk-go https://github.com/postgrip-io/agent-sdk-go/tree/main{/dir} https://github.com/postgrip-io/agent-sdk-go/blob/main{/dir}/{file}#L{line}">
<meta http-equiv="refresh" content="0; url=https://github.com/postgrip-io/agent-sdk-go">
</head>
<body>
Redirecting to <a href="https://github.com/postgrip-io/agent-sdk-go">github.com/postgrip-io/agent-sdk-go</a>.
</body>
</html>
```

The `meta http-equiv="refresh"` line is for humans landing on the page in
a browser — it bounces them to the GitHub repo. `go get` ignores it.

## Hosting options

Any static-page host works. Common choices:

- **Cloudflare Pages / GitHub Pages / Netlify / S3 + CloudFront** — push
  the HTML and a `_redirects` (or equivalent) rule that serves the same
  page for every path under `/sdk/`.
- **Custom HTTP service** (Go binary using `net/http`, ~30 lines) — if
  you already have a server fleet and want one less external dependency.

## Verifying

Once the page is live:

```sh
curl -sI https://go.postgrip.io/sdk?go-get=1            # should be 200
curl -s 'https://go.postgrip.io/sdk?go-get=1' | head    # should include go-import meta
go get go.postgrip.io/sdk/client                        # in a fresh Go module
```

The first `go get` fetch may take several seconds while Go's module proxy
caches the result. Subsequent fetches are fast.

## Tagging releases

The vanity path resolves regardless of tag, but customers should pin to a
released version:

```sh
go get go.postgrip.io/sdk/client@v0.1.0
```

Tags must be pushed to the GitHub repo (the *upstream* the vanity points
at). The vanity host doesn't serve tarballs — Go's proxy fetches from the
git URL in the `go-import` tag.
