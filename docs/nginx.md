# nginx reverse proxy & caching

Deep Reader is meant to sit behind a reverse proxy that terminates TLS (HTTPS is
required for Service Workers and PWA installation). The Go binary binds only to
`127.0.0.1:8080` and serves both the API and the embedded SvelteKit build from a
single origin.

This document explains how to configure **HTTP caching** correctly. Getting it
wrong does not just waste bandwidth — it breaks PWA updates.

## The problem: "App update available" never goes away

The embedded static server (Fiber's `static` middleware in
`internal/api/static.go`) stamps **every** static response with:

```
Cache-Control: public, max-age=31536000
```

A one-year cache is correct for content-hashed build assets, but it is wrong for
the two files that drive updates:

- **`index.html`** — the SPA entry point. It is *not* fingerprinted and it
  references the hashed `/_app/immutable/*` bundles of the current build.
- **`service-worker.js`** — its body embeds the build version
  (`shell-<version>`) and the precache manifest.

When `index.html` is pinned in the browser's HTTP cache for a year, deploying a
new build does not help: the browser keeps serving the **old** HTML, which
points at the **old** asset hashes, so the app never actually advances. The new
service worker installs, the banner appears, the user taps **Обновить**, the
page reloads — and the proxy hands back the stale cached `index.html` again. The
banner reappears on the next check. Clearing the Cache Storage or unregistering
the worker does not fix it, because the stale copy lives in the *HTTP* cache.

The fix is to revalidate the HTML shell and the service worker on every request,
while keeping the long cache only for fingerprinted assets.

## Caching rules

| Path | `Cache-Control` | Why |
|---|---|---|
| `/_app/immutable/*` | `public, max-age=31536000, immutable` | Content-hashed by SvelteKit; the URL changes when the content changes, so it is safe to cache forever. |
| `/service-worker.js` | `no-cache` | Must be revalidated so a new version is detected promptly. |
| `/manifest.webmanifest` | `no-cache` | Small, infrequently changed, must not go stale. |
| `/icons/*`, `/robots.txt` | `public, max-age=3600` | Not fingerprinted; a short cache is fine. |
| `/api/*` | *(pass upstream through)* | The backend sets its own headers — `/api/articles/:id` is `immutable`, `/api/config` is uncached. Do **not** override. |
| everything else (the HTML shell / SPA routes) | `no-cache` | `index.html` must always reflect the latest build's asset hashes. |

`no-cache` means "store it, but revalidate with the server before reuse" — not
"do not store". The HTML and worker are tiny, so the revalidation cost is
negligible.

## nginx configuration

The Go app already emits `Cache-Control`, so the proxy must **replace** that
header per location with `proxy_hide_header` + `add_header`. (`add_header` alone
would append a second, conflicting value.)

```nginx
upstream deep_reader {
    server 127.0.0.1:8080;
}

# Shared proxy settings — keep these consistent across every location that
# proxies to the app so TRUST_PROXY / client-IP logging work correctly.
proxy_set_header Host              $host;
proxy_set_header X-Real-IP         $remote_addr;
proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;

server {
    listen 443 ssl;
    http2 on;
    server_name dr.tinyops.ru;

    # --- TLS ---------------------------------------------------------------
    ssl_certificate     /etc/letsencrypt/live/dr.tinyops.ru/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/dr.tinyops.ru/privkey.pem;

    # Service Workers need a generous body size for article payloads.
    client_max_body_size 10m;

    # --- Content-hashed build assets: cache forever ------------------------
    location ^~ /_app/immutable/ {
        proxy_pass http://deep_reader;
        proxy_hide_header Cache-Control;
        add_header Cache-Control "public, max-age=31536000, immutable" always;
    }

    # --- Service worker: always revalidate ---------------------------------
    location = /service-worker.js {
        proxy_pass http://deep_reader;
        proxy_hide_header Cache-Control;
        add_header Cache-Control "no-cache" always;
    }

    # --- PWA manifest: always revalidate -----------------------------------
    location = /manifest.webmanifest {
        proxy_pass http://deep_reader;
        proxy_hide_header Cache-Control;
        add_header Cache-Control "no-cache" always;
    }

    # --- Non-fingerprinted static assets: short cache ----------------------
    location ~* ^/(icons/|robots\.txt$|favicon) {
        proxy_pass http://deep_reader;
        proxy_hide_header Cache-Control;
        add_header Cache-Control "public, max-age=3600" always;
    }

    # --- API: preserve the backend's own caching headers -------------------
    location /api/ {
        proxy_pass http://deep_reader;
        # No override: /api/articles/:id is immutable, /api/config is uncached.
    }

    # --- HTML shell and all SPA routes: always revalidate ------------------
    location / {
        proxy_pass http://deep_reader;
        proxy_hide_header Cache-Control;
        add_header Cache-Control "no-cache" always;
    }
}

# Redirect plain HTTP to HTTPS.
server {
    listen 80;
    server_name dr.tinyops.ru;
    return 301 https://$host$request_uri;
}
```

Notes:

- The `always` flag on `add_header` ensures the header is also set on non-2xx
  responses (e.g. the `304 Not Modified` that revalidation produces).
- Location matching priority handles the overlap automatically: exact (`=`) and
  prefix (`^~`) matches win over the regex block, which wins over the catch-all
  `location /`. So `/service-worker.js` and `/_app/immutable/*` are never caught
  by the `no-cache` fallback.
- If you front nginx with a CDN, apply the same matrix at the CDN tier and make
  sure it forwards (does not collapse) the `Cache-Control` you set here.

## Verify

After reloading nginx (`nginx -t && systemctl reload nginx`), confirm each class
of file carries the right header:

```sh
# HTML shell — must revalidate
curl -sI https://dr.tinyops.ru/ | grep -i cache-control
# -> cache-control: no-cache

# Service worker — must revalidate
curl -sI https://dr.tinyops.ru/service-worker.js | grep -i cache-control
# -> cache-control: no-cache

# Hashed asset — cached for a year
curl -sI https://dr.tinyops.ru/_app/immutable/entry/start.*.js | grep -i cache-control
# -> cache-control: public, max-age=31536000, immutable
```

Then, in the browser: open DevTools → Application → Service Workers, tick
**Update on reload**, hard-reload once to flush the stale `index.html`, and the
update banner should clear and stay cleared across subsequent deploys.

## The app already sets these headers

As of the per-path fix in `internal/api/static.go` (`cacheControlFor`), the Go
server emits exactly the matrix above on its own: `/_app/immutable/*` is
`immutable`, while `index.html` and `service-worker.js` are `no-cache`. So a
plain reverse proxy that forwards upstream headers untouched is already correct —
**the `proxy_hide_header` / `add_header` overrides in the config above are
optional.**

Keep the overrides only if you want the edge to be authoritative regardless of
the backend (e.g. a shared nginx template, or a CDN tier that must not inherit a
surprising `Cache-Control`). If you drop them, make sure nginx does **not** add
its own `expires` / `Cache-Control` for these locations, and that any CDN
forwards the backend's `Cache-Control` rather than overriding it.
