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
  comments/         discussion-thread sources (Hacker News) + extractor routing
  enrich/           LLM-driven CEFR-tuned enrichment
  llm/              OpenAI-compatible client, worker pool
  normalize/        text normalization
  publish/          public article pages (HTML + Open Graph rendering, file store)
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

## Accessibility

The user-facing summary is in [README.md](README.md#accessibility); this is what
you need to know before touching the code behind it.

**The reader is a composite widget, not a page of links.** `TokenRenderer`
exposes only *annotated* tokens (enrichment difficult word, enrichment phrase,
vocabulary-overlay match) as `role="button"`, and gives exactly one of them
`tabindex="0"` — a roving tabindex, moved by `←`/`→`. The set is built by
`buildInteractiveIndices` in `reader-utils.ts`, which must stay in step with
`resolveClickContent`: anything a click can open must be reachable from the
keyboard, and nothing else may become a stop. One stop per phrase, not per token.
The single exception is phrase-selection mode (`phraseAnchor` set, see
`WORD-CACHE-ARCH.md` §18): while the user is picking a phrase, *every* token is
a valid endpoint, so every token becomes a button and joins the roving set —
`←`/`→` then walk word by word and `Enter` closes the range.

**Translated text carries `lang`.** The document is `lang="en"`, so any run in
the user's target language (`Settings.target_language`, a BCP 47 tag) needs its
own `lang` — otherwise a screen reader pronounces Russian with an English voice,
which is not an accent but noise. Sites: `WordPopover`, `SentenceSheet`, the
reader's glossary, `WordRow`. The generated public page already does this
(`publish.Page.Lang` / `SourceLang`). Article summaries are written in English
and must NOT be tagged.

**`--reader-accent`, not `--primary`, for accented text in the article.**
`--primary` is tuned as a button fill behind white text and reaches only 3.4:1
as ink on the dark background. `--reader-accent` tracks `--primary` in light and
sepia and is overridden in `.dark`.

**Motion.** `app.css` collapses animations and scroll behaviour under
`prefers-reduced-motion`. That cannot reach a script that asks for
`behavior: 'smooth'` explicitly, so JS scrolling goes through
`scrollBehavior()` in `$lib/a11y.ts`.

`svelte-check` enforces the compiler's a11y rules and `just lint` fails on them;
a `svelte-ignore` needs a comment saying why the checker is wrong.

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

### Comment threads

A discussion-thread URL is not extracted as a page. `internal/comments` sits in
front of the article extractors and routes a URL a comment source recognises to
that site's own API; everything else falls through to the markdown.new →
readability chain unchanged. Threads therefore cost no markdown.new budget, and
a thread fetch never falls back to scraping the page (readability would store
the navigation chrome of a thread page as if it were the discussion) — a failure
is a normal `fetch_failed` the enrichment pool retries.

Supported today:

| Source | URLs | API |
|---|---|---|
| Hacker News | `https://news.ycombinator.com/item?id=<id>` — a story or a single comment permalink | `https://hn.algolia.com/api/v1/items/<id>`, one request per thread |

The whole thread is ingested, at any depth. It is stored as Markdown
(`content_format=markdown`) with the reply structure encoded as blockquote
nesting: a top-level comment is unquoted, a reply carries one `>` marker, its
reply two. Each comment opens with an `h4` author line whatever its depth —
heading level is not used for nesting, because it stops at `h6` and only changes
the size of a name, which is what made a thread read as a flat list. The reader
(`markdown-blocks.ts` → `nestBlocks`) turns each level into an indented column
with a rail, so who answers whom is visible while every word stays tappable.
Nesting is capped at 6 levels — deeper and a phone has no width left for the
text — and past the cap the author line carries a chevron per extra level.
Comment bodies are converted from HN's HTML subset
— quotes, emphasis, code blocks and links survive; a bare link is labelled with
its host so the URL is not tokenized into junk words. A deleted comment is
skipped while its replies are kept. The story's linked page is offered as a
link, never fetched: one card is one text.

In the reader each comment is a foldable branch: the author line carries a
chevron, and folding hides the comment's body together with every reply under it
(the count is shown so a folded branch says how much it hides). The fold set is
persisted per article — locally in IndexedDB (`thread_collapse`) and on the
server (`PUT /api/articles/:id/collapse`, delivered back in the `collapsed` list
of `GET /api/config`), merged Last-Write-Wins on `updated_at` exactly like
reading progress, so a branch folded offline on a phone survives and reaches the
desktop on the next sync. A branch is identified by the token index of the first
word of its author line, which is stable across devices and byte encodings.
Deleting an article removes its folds: through the FK cascade on the server, in
`enqueueDelete` locally, and in the full-sync reconciliation for an article
deleted on another device. A published page always shows the whole thread —
folding is a reading state, not part of the document.

An ingested thread is stamped `source_type=comments` (articles and pasted text
are `article`), which is what the library uses for the discussion icon and the
articles/discussions filter. There are no environment variables: the thread
fetch reuses `READABILITY_TIMEOUT`. A brand-new thread the search API has not
indexed yet answers 404, which is retried with the pipeline's usual back-off.

Adding another site (Reddit, Lobsters, …) means implementing `comments.Source`
and registering it in `cmd/server`; nothing else in the pipeline changes.

## Vocabulary and saved words

Every word or phrase you tap for a translation is recorded automatically and fed
back into reading — the full design is in
[WORD-CACHE-ARCH.md](WORD-CACHE-ARCH.md). Two things about it are worth knowing
before touching the code.

**Manual saving** (`WORD-CACHE-ARCH.md` §18) lets the reader collect a word the
LLM never annotated: long-press (or right-click) any token → *Save “word”*, or
*Save phrase…* and then tap the phrase's other end. It writes into the same
`vocab_entries` aggregate as a passive tap, so the overlay, `/words`, the
enrichment filter and the delta sync need no special case.

**`POST /api/translate`** is what supplies the translation for such a term:

```
POST /api/translate   {kind, text, lemma, context} -> {translation, cefr_level?, phrase_type?}
```

It is the only LLM call a client can trigger, so note:

- It is **rate-limited to 60 requests/minute** (`defaultTranslateMax` in
  `internal/api/api.go`), and the limiter is registered *left of* the handler.
- Its status codes are a contract with the client's outbox: a 4xx makes the
  queued save go out **untranslated**, a 5xx/429 makes it retry. A provider
  failure must therefore answer 502, never 400.
- The prompt is user-editable in Settings → LLM (`settings.translate_prompt`,
  empty = `llm.DefaultTranslatePromptTemplate`); placeholders
  `{{target_language}}` and `{{cefr_level}}`.
- **Saving works offline.** Nothing in the save path touches the network: the
  entry is written to Dexie and the outbox immediately, and the drain — woken by
  the `online` event, the 60s foreground interval, or resume-from-background —
  fills in the translation later. The pending state is worded by connectivity
  (`vocab/pending.ts`, driven by `syncStatus.online`): "Translating…" when
  online, "will translate when you're back online" when not. Keep that
  distinction if you touch the copy — a spinner-ish label with no network reads
  as a hung app.

## Public pages

An enriched article can be published under an unguessable link
(`/p/<token>`) that anyone can open without an account. The published page
carries only the **sentence translations** — no tapping, no word overlay, no
offline cache — plus the title, description and source attribution. Stretches
the LLM never covered are carried over in the original language rather than
dropped.

A published page reproduces the structure of the source. For a Markdown article
— which is what a comment thread is (see [Comment threads](#comment-threads)) —
`internal/publish` parses the original text into blocks before laying out the
translations, so the page nests each reply level in an indented column with a
rail, prints the author line of every comment as a label, and keeps code blocks
monospaced. Markers never reach the page as text: only the visible range of each
line is read, and inline emphasis / link syntax in untranslated stretches is
stripped (a link keeps its label, not its URL). Lists and tables are the
exception — they are laid out as paragraphs, markers included.

The page is rendered **once, at publish time**, into a standalone HTML file with
its Open Graph metadata inlined, so link previews work without any server-side
rendering: serving a page is a TTL check plus a file read. Publishing again
mints a new token and invalidates the previous link.

Link lifetime is set in **Settings > Public Pages** (in hours; `0` means the
link never expires) and is stamped into each link when it is created, so
changing the setting never affects links already shared. Expired links answer
404, and a sweeper removes their files hourly — along with pages orphaned by a
deleted article.

| Variable | Default | Purpose |
|---|---|---|
| `PUBLIC_PAGES_DIR` | `public-pages` next to `DATABASE_PATH` | Where generated pages are stored. Must be on the persistent volume. |
| `PUBLIC_BASE_URL` | empty | Externally reachable origin for share links and `og:url`. Empty derives it from the publishing request. |

Endpoints: `POST|GET|DELETE /api/articles/:id/publish` (authenticated) and
`GET /p/:token` (public).

## Passkeys (WebAuthn)

Passkeys are an **additional** way to sign in — Face ID, Touch ID, a device PIN
or a security key instead of typing the password. The password always keeps
working, so losing every device cannot lock the account out.

Sign-in is **discoverable** ("usernameless"): registration requires a resident
key and user verification, and the sign-in challenge names no credentials, so
the authenticator offers the account itself. Because of that the `/login` page
shows a single *Sign in with a passkey* button and asks for nothing else.

The credential is stored as the JSON the WebAuthn library produced
(`webauthn_credentials.credential`) plus a stable, opaque 32-byte user handle on
the account (`app_user.webauthn_user_handle`, minted with `randomblob(32)` when
the account is created). The handle is what makes a discoverable login resolve
to this account, so it is never rotated. Challenge state lives in memory for
five minutes, is consumed on first use, and is capped at 64 live ceremonies —
the sign-in "begin" endpoint is unauthenticated, so an unbounded cache would be
a remote memory-growth vector.

**The relying-party ID cannot be derived per request.** A credential registered
under one RP ID is unusable under another, so it is deployment configuration,
not something read off the `Host` header. Changing `PASSKEY_RP_ID` invalidates
every registered passkey. Passkeys also need HTTPS (localhost is exempt).

| Variable | Default | Purpose |
|---|---|---|
| `PASSKEY_ENABLED` | `true` | Master switch. Still requires a resolvable RP ID. |
| `PASSKEY_RP_ID` | host of `PUBLIC_BASE_URL` | Bare registrable domain credentials bind to (no scheme, no port). Empty **and** no `PUBLIC_BASE_URL` disables passkeys with a startup warning. |
| `PASSKEY_RP_NAME` | `Deep Reader` | Name shown in the platform's passkey prompt. |
| `PASSKEY_RP_ORIGINS` | `https://<PASSKEY_RP_ID>` + the `PUBLIC_BASE_URL` origin | Comma-separated allowlist of origins permitted to run a ceremony. Set explicitly for http development. |
| `PASSKEY_IOS_APP_ID` | empty | `<TeamID>.<BundleID>` published in `/.well-known/apple-app-site-association`. Empty ⇒ that endpoint 404s. |
| `PASSKEY_ANDROID_PACKAGE` | empty | Android application id published in `/.well-known/assetlinks.json`. |
| `PASSKEY_ANDROID_FINGERPRINTS` | empty | Comma-separated SHA-256 signing-certificate fingerprints (keytool format). |
| `PASSKEY_IOS_ENABLED` | `false` | **Build-time only** (read by the Justfile). Adds the iOS Associated Domains entitlement during `just cap-sync`; needs a paid Apple Developer Program membership. See [docs/MOBILE.md](docs/MOBILE.md). |

Endpoints — management (authenticated): `GET /api/passkeys`,
`POST /api/passkeys/register/begin`, `POST /api/passkeys/register/finish`,
`PATCH /api/passkeys/:id`, `DELETE /api/passkeys/:id`. Sign-in (public,
rate-limited to 30/min): `POST /api/passkeys/login/begin`,
`POST /api/passkeys/login/finish`. `GET /api/config` reports
`auth.passkey_enabled` so the client hides every passkey affordance on a server
without an RP ID. Every passkey route answers **501** when none is configured.

A rejected assertion deliberately does **not** feed the per-IP password lockout:
a passkey is not a guessable credential, and counting it would let anyone lock
the account out of its password by spamming bad assertions. The endpoint's own
rate limiter is what bounds the cost.

Manage passkeys in **Settings > Security**.

### Local development

Passkeys need the RP ID to match the origin the page is served from, so a dev
run over http needs both values spelled out:

```sh
PASSKEY_RP_ID=localhost
PASSKEY_RP_ORIGINS=http://localhost:4200,http://localhost:18080
```

### Native apps

A native app is not a web origin, so the OS asks the server to vouch for it. The
backend serves both association documents itself — nothing goes next to the
reverse proxy:

| Path | Source | Read by |
|---|---|---|
| `/.well-known/apple-app-site-association` | `PASSKEY_IOS_APP_ID` | iOS (webcredentials only; app links are deliberately not declared) |
| `/.well-known/assetlinks.json` | `PASSKEY_ANDROID_PACKAGE` + `PASSKEY_ANDROID_FINGERPRINTS` | Android |

Android's Credential Manager reports a native caller as
`android:apk-key-hash:<base64url(sha256(signing cert))>` rather than as an
https origin. Those facets are derived from `PASSKEY_ANDROID_FINGERPRINTS` and
appended to the accepted-origin list automatically, so Android passkeys silently
fail origin validation if the fingerprints are missing.

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
