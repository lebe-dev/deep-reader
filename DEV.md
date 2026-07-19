# DEV.md — developer guide

Backend: Go (Fiber, SQLite). Frontend: SvelteKit (`adapter-static`), embedded into
the Go binary via `go:embed` for production. Every workflow goes through `just`
(see `Justfile`).

## Local development

Copy the env template and fill in at minimum `LLM_API_KEY` and `LLM_API_BASE_URL`:

```sh
cp .env.example .env
```

Authentication is not configured via env. On first launch the app redirects to
`/setup`, where you create the single built-in account (username + password). The
password is stored as a bcrypt hash in the database, and every device signs in
with that same account.

Run the backend (from the repo root):

```sh
just run-backend
```

Run the SvelteKit dev server (separate terminal, with HMR):

```sh
just run-frontend
```

The frontend dev server proxies API requests to the Go backend. The two servers
run on different ports during development; in production they share a single
origin because the frontend is embedded into the Go binary.

```sh
just stop   # kill anything still bound to the dev ports
```

## Project structure

```
cmd/server/        entrypoint
internal/
  api/              HTTP handlers (Fiber), static file serving
  auth/             login, session, bcrypt, per-IP lockout
  ingest/           article URL fetching pipeline
  extract/          content extraction (readability)
  markdown/         markdown.new client + daily budget tracking
  enrich/           LLM-driven CEFR-tuned enrichment
  llm/              OpenAI-compatible client, worker pool
  normalize/        text normalization
  tokenize/         sentence/word tokenization
  store/            SQLite persistence
  model/            domain types
  config/           env-driven configuration
  obs/              logging, Sentry
  ports/, deps/     interfaces and dependency wiring
  version/          build-time version string
frontend/         SvelteKit app (see frontend/src/lib for api.ts, db.ts, sync/)
web/web.go        go:embed of frontend/build into the binary
docs/             nginx.md, MOBILE.md
```

## Production build

Build a single self-contained binary with the frontend embedded:

```sh
just build          # build-frontend -> embed -> go build
./bin/deep-reader    # or: just run-backend after setting env vars
```

`just build` runs `npm install && npm run build` in `frontend/`, copies the output
into `web/dist/`, then compiles the Go binary. The `go:embed` directive in
`web/web.go` bakes `web/dist/` into the binary at compile time, so no separate
static-file directory is needed at runtime.

## Deploy with Docker Compose

`docker-compose.yml` pulls a prebuilt image by default (`tinyops/deep-reader:<VERSION>`):

```sh
cp .env.example .env       # set LLM_API_KEY, LLM_API_BASE_URL, etc.
just start-env              # docker compose up -d
just logs                   # tail logs
```

To build and use your own image instead, uncomment the `build:` block in
`docker-compose.yml`, or build/push a tagged image with:

```sh
just build-image           # go test + lint, then docker build
just push-image             # push tinyops/deep-reader:<VERSION>
```

SQLite data is persisted in `./data/` on the host. The service binds only to
`127.0.0.1:8080`; a reverse proxy (Caddy, nginx, Traefik) on the host must
provide HTTPS — TLS is required for Service Workers and PWA installation to work
on all browsers. See [docs/nginx.md](docs/nginx.md) for the caching rules the
proxy must respect (getting them wrong breaks PWA updates).

## Linting and tests

```sh
just lint            # go vet + golangci-lint + eslint + svelte-check
just lint-backend
just lint-frontend

just test            # go test ./... + frontend check/vitest
just test-backend [name]   # -run "<name>" when given
just test-frontend

just coverage        # coverage.out + coverage.html
```

## Content extraction

Article content is extracted before enrichment. By default Deep Reader uses
[markdown.new](https://markdown.new) as the **primary** extractor: it converts a
URL into clean Markdown that tokenizes and enriches better than raw HTML (and
renders JS-heavy pages in a headless browser). The built-in readability
extractor is the **fallback**, used automatically when markdown.new fails or
when the daily budget is exhausted — so adding articles never hard-fails.

The free markdown.new plan grants **500 request units per day per IP**,
resetting at UTC midnight. Deep Reader tracks consumption in SQLite and enforces
a local budget so it can warn you before the service starts rejecting requests.
With the default `MARKDOWN_COST_PER_ARTICLE=50` that is roughly **10
conversions/day**; once spent, extraction transparently falls back to
readability until the next reset. The remaining daily budget is shown in the
"Add article" dialog and returned by `GET /api/config` as `markdown_budget`.

| Variable | Default | Purpose |
|---|---|---|
| `MARKDOWN_ENABLED` | `true` | Use markdown.new as the primary extractor. Set `false` to use readability only. |
| `MARKDOWN_BASE_URL` | `https://markdown.new` | Service base URL (override for a self-hosted instance). |
| `MARKDOWN_TIMEOUT` | `45s` | Timeout for a single conversion. |
| `MARKDOWN_DAILY_LIMIT` | `500` | Request-unit budget per UTC day (`0` = unlimited). |
| `MARKDOWN_COST_PER_ARTICLE` | `50` | Request units charged per article conversion. |

## Error tracking (Sentry)

Sentry is optional and **off by default** — it reports errors and panics only
(no performance tracing) and is enabled per side by setting a DSN. Backend and
frontend are typically separate Sentry projects.

The frontend DSN is delivered to the browser at runtime via `GET /api/config`,
not baked at build time: the static PWA is built once and embedded in the
binary, so configuration must come from the deployment's environment. Browser
DSNs are public by design, so this is not a secret. Because of the runtime
handshake, errors thrown during the very first page load (before
`/api/config` returns) are not captured.

| Variable | Default | Purpose |
|---|---|---|
| `SENTRY_DSN` | empty | Backend (Go) DSN. Empty disables backend reporting. |
| `SENTRY_FRONTEND_DSN` | empty | Browser DSN, sent to the client via `GET /api/config`. Empty disables frontend reporting. |
| `SENTRY_ENVIRONMENT` | empty | Environment tag (e.g. `production`) applied to both. |

The release for both SDKs is the server version, so frontend and backend events
line up with the same release.

## Mobile apps

Building and sideloading the iOS/Android apps is a separate workflow — see
[docs/MOBILE.md](docs/MOBILE.md).
