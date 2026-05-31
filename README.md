# Deep Reader

A self-hosted PWA for reading English-language articles with partial AI-assisted translation tuned to your CEFR proficiency level. Add an article URL, let the backend extract and enrich it via an OpenAI-compatible LLM, then read offline on any device — tap words and phrases to get in-context translations without a network connection.

## Local development

Copy the env template and fill in at minimum `AUTH_TOKEN`, `LLM_API_KEY`, and `LLM_API_BASE_URL`:

```sh
cp .env.example .env
```

Run the backend (Go, from the repo root):

```sh
just be-run
```

Run the SvelteKit dev server (separate terminal, with HMR):

```sh
just fe-dev
```

The frontend dev server proxies API requests to the Go backend. The two servers run on different ports during development; for production they share a single origin because the frontend is embedded into the Go binary.

## Production build

Build a single self-contained binary with the frontend embedded:

```sh
just build          # fe-build -> embed -> be-build
./bin/deep-reader   # or: just be-run after setting env vars
```

`just build` runs `npm run build` in `frontend/`, copies the output into `web/dist/`, then compiles the Go binary. The `go:embed` directive in `web/web.go` bakes `web/dist/` into the binary at compile time, so no separate static-file directory is needed at runtime.

## Deploy with Docker Compose

```sh
cp .env.example .env   # set AUTH_TOKEN, LLM_API_KEY, etc.
just docker-build      # builds the multi-stage image
just up                # docker compose up -d
just logs              # tail logs
```

SQLite data is persisted in `./data/` on the host. The service binds only to `127.0.0.1:8080`; a reverse proxy (Caddy, nginx, Traefik) on the host must provide HTTPS — TLS is required for Service Workers and PWA installation to work on all browsers.

## Content extraction

Article content is extracted before enrichment. By default Deep Reader uses [markdown.new](https://markdown.new) as the **primary** extractor: it converts a URL into clean Markdown that tokenizes and enriches better than raw HTML (and renders JS-heavy pages in a headless browser). The built-in readability extractor is the **fallback**, used automatically when markdown.new fails or when the daily budget is exhausted — so adding articles never hard-fails.

The free markdown.new plan grants **500 request units per day per IP**, resetting at UTC midnight. Deep Reader tracks consumption in SQLite and enforces a local budget so it can warn you before the service starts rejecting requests. With the default `MARKDOWN_COST_PER_ARTICLE=50` that is roughly **10 conversions/day**; once spent, extraction transparently falls back to readability until the next reset. The remaining daily budget is shown in the "Add article" dialog and returned by `GET /api/config` as `markdown_budget`.

| Variable | Default | Purpose |
|---|---|---|
| `MARKDOWN_ENABLED` | `true` | Use markdown.new as the primary extractor. Set `false` to use readability only. |
| `MARKDOWN_BASE_URL` | `https://markdown.new` | Service base URL (override for a self-hosted instance). |
| `MARKDOWN_TIMEOUT` | `45s` | Timeout for a single conversion. |
| `MARKDOWN_DAILY_LIMIT` | `500` | Request-unit budget per UTC day (`0` = unlimited). |
| `MARKDOWN_COST_PER_ARTICLE` | `50` | Request units charged per article conversion. |
