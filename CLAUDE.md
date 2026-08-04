# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PlayMore is a self-hosted game publishing platform for HTML5 games (Go 1.26 + Gin + SQLite). Single binary deployment with embedded frontend via `go:embed`. Pure Go SQLite driver, **no CGO**.

## Commands

```bash
go build -o playmore                                      # Build
go vet ./...                                              # Only linter in the repo
./playmore setup                                          # Interactive .env wizard
./playmore                                                # Run (reads .env, then flags/env)
./playmore --port 3000 --data /path/to                    # Custom port and data dir
./playmore --tls-cert cert.pem --tls-key key.pem          # Direct TLS (TLS 1.3 only)
./playmore --auto-tls --domain playmore.example.com       # Let's Encrypt (needs :80 + :443)
./playmore --smtp-host ... --smtp-user ... --base-url ... # Enable email (verify / reset)
./playmore --trusted-proxies '127.0.0.1/32,10.0.0.0/8'    # REQUIRED behind reverse proxy
./playmore --behind-tls-proxy                             # Force Secure-cookie when proxy terminates TLS
./playmore --games-domain games.example.com               # Serve /play/* from separate origin (sandboxing)
./playmore --stun-servers ... --turn-servers ...          # WebRTC ICE config, exposed via GET /rtc-config
./playmore --uploads-gc --uploads-gc-dry-run              # Preview the unreferenced-uploads sweep before enabling
docker-compose up -d                                      # Docker deployment
```

Seeding demo data now requires an authenticated session (`POST /api/v1/seed`, admin-gated) — it is no longer an anonymous curl.

Config precedence: **CLI flag > env var > `.env` file**. Env-var fallbacks are the flag name upcased with a `PLAYMORE_` prefix (`PLAYMORE_PORT`, `PLAYMORE_DATA`, `PLAYMORE_BASE_URL`, `PLAYMORE_GAMES_DOMAIN`, `PLAYMORE_TRUSTED_PROXIES`, `PLAYMORE_BEHIND_TLS_PROXY`, `PLAYMORE_STUN_SERVERS`, `PLAYMORE_TURN_SERVERS`, `PLAYMORE_SMTP_{HOST,PORT,USER,PASS,FROM}`, …). `.env` is loaded at startup via `loadEnvFile()` and never overrides existing env. Full table in `AGENTS.md`.

## Testing

There **is** an automated suite and CI (`.github/workflows/test.yml` runs `go test ./...` plus `sdk-go` on every push/PR).

```bash
go test ./...                                             # Everything (~25s; bcrypt dominates)
go test ./internal/handlers/ -run TestGameSaves -v         # One package / one test
go test ./internal/server/ -run TestMountAPIRoutes_OpenAPIDrift  # The route-doc gate
cd sdk-go && go test ./...                                # sdk-go is a SEPARATE Go module
scripts/verify-chunked-upload.sh                          # Chunked-upload E2E (needs sqlite3, python3+bcrypt, zip)
go run ./cmd/mp-test                                      # Multiplayer lobby E2E harness
```

- **`internal/testutil`** is the HTTP-level harness: `testutil.NewTestServer(t)` wires the *real* Gin router (`server.MountAPIRoutesForTest`) onto a temp-file SQLite DB with the real schema + migrations. Use `ts.Do(t, method, path, body, opts...)` / `ts.DoMultipart` with `WithAuth(user)`, `WithAPIKey(user)`, `WithIP`, `WithSameOrigin`; seed with `testutil.SeedUser/SeedGame/SeedBuild`. Prefer this over mocks — the point is to exercise middleware → handler → DB.
- It swaps the package-level `storage.DB`, so **tests in a package must not run in parallel**. Call `testutil.ResetRateLimits()` (or use `WithIP`) to keep per-IP limiters from bleeding between tests.
- Handler tests live in `package handlers_test` (external) to avoid the `handlers → server → handlers` import cycle; `routeindex_test.go` is internal (`package handlers`).
- Coverage is partial. Still exercise new UI flows manually in a browser — there's no frontend test setup.

## Architecture

```
main.go                    # Entry, CLI flags, .env loader, setup wizard, go:embed frontend,
                           #   SMTP health check, background workers, graceful shutdown
internal/
  server/server.go         # Gin engine: recovery, client-IP resolution, logger, trusted proxies,
                           #   HTTPS redirect, security headers, CORS, gzip, health checks,
                           #   root-mounted routes (/ws, /play, /docs, /uploads, /avatar, /deploy.sh)
  server/routes.go         # mountAPIRoutes() — the entire /api route table + per-route middleware
  server/spa.go            # NoRoute SPA fallback: per-request CSP nonce injection, games origin
  handlers/                # HTTP handlers (see below for the newer subsystems)
  handlers/openapi.yaml    # Hand-written OpenAPI 3 spec — CI fails if it drifts from the routes
  handlers/routeindex.go   # Route-table ↔ OpenAPI drift + schema-completeness checkers
  handlers/playmore-mp.js  # Embedded multiplayer client SDK, served at /playmore-mp.js
  handlers/playmore-deploy.sh  # Embedded shell script served at /deploy.sh
  models/                  # DB queries, one file per entity
  storage/db.go            # SQLite schema + idempotent migrations, Schema()/Migrations() for tests
  storage/files.go         # Game file storage, ZIP extraction with path traversal protection
  storage/partial.go       # Chunked-upload partial-file assembly
  middleware/              # auth.go (session + pm_k_/pm_gk_/pm_gs_ Bearer), ratelimit.go, csrf.go,
                           #   cors.go (opaque-origin game iframes), clientip.go, analytics.go,
                           #   loginbackoff.go (per-IP+account exponential backoff)
  lobby/                   # Multiplayer lobby hub: hub.go, protocol.go, persist.go (async SQLite)
  webhook/dispatcher.go    # Outbound webhook queue: buffered channel + worker, HMAC-SHA256 signing
  uploadgc/gc.go           # Periodic sweeps: expired upload sessions + opt-in uploads/ prune
  email/email.go           # SMTP sender, health check, ProtonMail Bridge detection
  testutil/server.go       # Test harness (see Testing)
frontend/index.html        # Vanilla JS SPA (~4900 lines, inline CSS/JS). js/ and css/ are empty
                           #   placeholders — keep it single-file unless there's a strong reason
sdk-go/                    # Go client SDK — SEPARATE module, own go.mod, own CI job
npm/                       # npm package metadata for playmore-mp.js (published as `playmore-mp`)
cmd/mp-test/               # Multiplayer E2E harness (spins a real server in a temp dir)
docs/                      # SETUP.md, DEVELOPER.md, sdk/ (multiplayer SDK), superpowers/ (design docs)
scripts/                   # install.sh, deploy.sh, backup.sh, verify-chunked-upload.sh (not embedded)
v1/                        # Original single-file HTML prototype (archived; auto-deploys to GH Pages)
```

## API versioning — read before touching routes

`mountAPIRoutes(g, cfg)` in `internal/server/routes.go` is called **twice**: on `/api/v1` (canonical, where new endpoints belong) and on `/api` (permanent alias for pre-versioning clients). Two tests enforce this contract:

- `TestMountAPIRoutes_BothPrefixesAreEquivalent` — the two prefixes must expose identical `(method, path)` sets. Because both come from one function, this passes for free; it fails if someone hand-registers a route on only one prefix.
- `TestMountAPIRoutes_OpenAPIDrift` — every `/api/v1` route must have a matching entry in `internal/handlers/openapi.yaml`, and vice versa. **Adding a route without documenting it fails CI.** Root-mounted routes (`/ws`, `/health`, `/docs`, `/play/*`) are outside `/api` and exempt.
- `TestOpenAPISchemaIntegrity` / `TestOpenAPIDeveloperSurfaceFullyTyped` — 2xx responses on the developer surface need real schemas, and every `$ref` must resolve.

Per-group middleware (`GlobalRateLimit(600, 300)`, `AuthOptional`, `CSRFProtect`) is applied inside `mountAPIRoutes`, so both prefixes are protected identically without callers remembering.

## Key patterns

- **Auth (four credential types)**: `middleware.AuthOptional()` checks `Authorization: Bearer` first — `pm_k_` (user API key), `pm_gk_` (per-game SDK key, long-lived), `pm_gs_` (per-game session token, 5-min TTL, minted by the SPA for the game iframe). An invalid Bearer **rejects immediately**. Falls back to the `session` HTTP-only cookie (SameSite=Lax). Game credentials cannot reach account endpoints — `AuthRequired()` rejects them; use `AuthRequiredOrGameSession()` for game-facing routes. Helpers: `GetUser`, `IsAPIKeyAuth`, `IsGameAuth`, `IsGameSessionAuth`, `GetGameAPIKey`, `GetGameSessionToken`, `IsSecure`. `handlers.RequireVerifiedEmail()` gates write paths.
- **CSRF**: Origin/Referer validation on state-changing requests, applied **after** AuthOptional so API-key requests skip it. The API only accepts JSON and multipart/form-data.
- **CORS**: `middleware.CORS()` is mounted at engine level (so OPTIONS preflights are answered for routes registering only GET/POST). It returns `*` for `/api/` paths because sandboxed game iframes send `Origin: null` — echoing an origin would break them. Safe because game-facing auth is Bearer-only, never cookies. `/auth/*`, `/admin/*`, `/seed`, `/settings/*`, `/api-keys*`, `/profile*` are excluded from the wildcard surface.
- **Admin**: First registered user is admin (lowest `created_at`). Admin endpoints return **404, not 403** to hide existence.
- **CSP**: Per-request 16-byte nonce generated in `server/spa.go`, injected into `<style>`/`<script>`. `script-src-attr 'none'` — the SPA has **no inline `on*=` handlers**. `style-src-attr 'unsafe-inline'` is still allowed. GoatCounter URL extends `script-src`/`connect-src`; `--games-domain` extends `frame-src`/`img-src`/`media-src`.
- **Frontend**: Single HTML file, all rendering via `innerHTML` template strings. `api(path, opts)` wraps `fetch()`. Hash routing (`#store`, `#game/<id>`, `#developer/<name>`) via `navigate(tab)`. Always pass user input through `escapeHtml()`. Theme via `data-theme` on `<html>`.
- **Events (no inline handlers)**: emit `' + act('fnName', ...args) + '` for clicks or `' + onEv('change'|'input'|'focus', 'fnName', ...args) + '` for other events instead of `onclick=`. These emit `data-act`/`data-<event>` + JSON-encoded, HTML-escaped `data-*-args`; delegated listeners dispatch to `window[fnName]` with `this` bound to the element. `closest()` resolves the innermost target (no `stopPropagation` needed). Hover/focus effects go in CSS.
- **Game serving**: `/play/:id/*filepath` via iframe. Every HTML response carries a *server-enforced* CSP `sandbox allow-scripts allow-pointer-lock allow-popups allow-forms allow-modals` (deliberately no `allow-popups-to-escape-sandbox`, no `allow-same-origin`) — this covers direct navigation, not just SPA-launched iframes. `frame-ancestors` gates embedding (XFO can't whitelist a cross-origin host, so split-origin needs CSP). `Permissions-Policy` delegates `webgpu=*` to the sandboxed frame while denying camera/mic/geolocation/etc. Gzip excludes `/play/` (Range support) and `/ws` (needs raw `http.Hijacker`).
- **Build channels**: Each game has `game_builds` rows on `internal`/`beta`/`stable` channels with one active build per channel. `/play/<id>?channel=beta` serves the non-stable build **to the owning developer only**. Uploads and re-uploads create builds; activate/rollback/delete via `/api/v1/games/:id/builds/*`. Retention never deletes the active build.
- **Chunked uploads**: `POST /uploads/init` → `PUT /uploads/:id/chunks` (8 MiB chunks, concurrent, resumable — status reports received ranges) → `POST /uploads/:id/finalize`. Partial assembly in `storage/partial.go`; abandoned sessions swept by `uploadgc`. Design doc: `docs/superpowers/specs/2026-05-21-chunked-upload-design.md`.
- **Cloud saves**: Game iframes run at an opaque origin (no localStorage/IndexedDB), so `/api/v1/games/:id/saves/:key` is their durable storage. Raw JSON ≤ 64 KiB per value, max 32 keys per (user, game); scoped per-user. Accepts session auth or `pm_gs_`.
- **Play sessions**: `play_sessions` tracks live sessions for analytics and the `online_players` count — open / heartbeat (SPA every 30s) / end, callable directly from the game iframe via CORS.
- **Multiplayer lobbies**: `GET /ws` (root-mounted, exempt from the OpenAPI drift test) upgrades via `coder/websocket`; `Accept`'s same-host Origin check is the CSRF equivalent (WS handshakes are GETs and bypass `CSRFProtect`). `internal/lobby` holds the hub (`lobby.Default`): create/join by 6-char code, ready check, host-only start, host migration, rejoin, spectators, opaque message relay. Limits are consts in `hub.go`. Lobby state is persisted to the `lobbies` table **asynchronously** (`persist.go` — a buffered channel, so the hub mutex is never held across a DB write) and restored on startup via `RestoreLobbies()`. Games opt in via the `multiplayer` column; the SPA bridges lobby⇄iframe with postMessage.
- **Webhooks**: `internal/webhook` — handlers call `Dispatch()` to enqueue; one worker goroutine fans out to subscribers with HMAC-SHA256 signatures and retries. Queue is 1024-deep and **drops on overflow** rather than blocking the publish path. Target URLs are SSRF-validated (`models.ValidateWebhookURL`); tests set `models.AllowPrivateWebhookTargets` to reach `httptest` servers.
- **Database**: SQLite via `modernc.org/sqlite` (pure Go), WAL mode, `SetMaxOpenConns(1)`, foreign keys ON. FTS5 on games (title, description, tags) with auto-indexing triggers. Migrations in `db.go::migrationsAll()` are idempotent — append, never edit existing entries. `storage.Schema()` / `storage.Migrations()` / `IsIdempotentMigrationError()` exist so `testutil` applies the exact production DDL.
- **Rate limiting**: Per-IP, per-endpoint, in-memory, plus `GlobalRateLimit` on the API group. Failed logins additionally get per-(IP, account) exponential backoff (`loginbackoff.go`) — deliberately *not* per-account alone, which was abusable as a targeted lockout DoS.
- **Analytics**: Page views written asynchronously via channel; `middleware.StartAnalyticsWriter()` batches every 5s or 50 records. 90-day retention.
- **Background workers** (all started in `main.go` ~line 358): `StartRateLimitCleanup`, `StartAnalyticsWriter`, `lobby.Default.RestoreLobbies` + `StartCleanup`, `webhook.Start`/`Stop`, `uploadgc.Start`.
- **Trusted proxies**: By default the server trusts NO proxy headers. `--trusted-proxies` with explicit CIDRs enables them; `0.0.0.0/0` and `::/0` **panic** at startup. `middleware.RealClientIP` runs before the logger so access logs and rate limits use the unspoofable IP.
- **HTTPS redirect**: Only when `X-Forwarded-Proto: http` is seen AND a trusted proxy is configured; redirects using `baseURL`, not `Host` (prevents Host-header injection). Must run before security headers.
- **TLS**: `MinVersion: tls.VersionTLS13` for both direct and auto-TLS. `--auto-tls` and `--tls-cert/--tls-key` are mutually exclusive.
- **Body size caps**: Per-route `http.MaxBytesReader` via `bodyLimit()` in `routes.go`. Game upload = `storage.MaxFileSize` + 32 MiB overhead; images 10 MiB; chunk PUT 8 MiB + 1 MiB headroom; global JSON 1 MiB. `r.MaxMultipartMemory = 32 MiB`.
- **Logger / recovery**: Custom `gin.LoggerWithFormatter` strips `RawQuery` (auth tokens land in URLs for verify/reset) and sanitizes `\r\n`. Custom recovery logs the panic + stack but does **not** dump headers (Gin's default leaks the session cookie). Don't swap either back to the Gin defaults.
- **Secure cookies**: `Secure` set when `IsSecure(c)` is true, or unconditionally with `--behind-tls-proxy`.
- **Email-required gating**: Without SMTP, users can't verify email, so `RequireVerifiedEmail()` blocks uploads/reviews/devlogs. Startup prints a `⚠ SMTP not configured` warning.

## Database tables

users, sessions, api_keys, audit_log, games, games_fts, game_builds, game_api_keys, game_session_tokens, game_saves, play_sessions, lobbies, reviews, library, wishlist, playtime, activity, developer_pages, devlogs, comments, follows, collections, notifications, game_views, page_views, user_achievements, email_tokens, upload_sessions, webhooks, webhook_deliveries

## Go style

- Standard `go fmt`. Handlers return JSON via `gin.H{}`. Early-return error handling. `sql.NullString` for nullable fields. Log errors with `log.Println()`.
- Module path: `github.com/yusufkaraaslan/play-more` (hyphenated, despite the dir being `playmore`). `sdk-go/` is its own module.
- Comments in this codebase explain *why* — especially security decisions. Match that when touching auth, CORS, CSP, or rate limiting.

## Adding routes / migrations

- **New route** → add to `mountAPIRoutes` in `internal/server/routes.go` (both prefixes get it automatically), pick the middleware chain (`AuthRequired`/`AuthRequiredOrGameSession`, `RequireVerifiedEmail`, explicit `RateLimit`, `bodyLimit`), **then add it to `internal/handlers/openapi.yaml` with a real response schema** or CI fails.
- **New schema change** → append to the migrations slice in `internal/storage/db.go::migrationsAll()`. Never edit `const schema` in a way that breaks existing DBs. Add the same DDL to `const schema` for fresh installs.
- **New handler test** → `package handlers_test`, `testutil.NewTestServer(t)`.

## Reference docs

- `AGENTS.md` — longer-form agent guide; authoritative flag/env table and feature inventory
- `docs/SETUP.md` — production config (HTTPS, email, systemd)
- `docs/DEVELOPER.md` — API keys, deploy CLI, full API reference
- `docs/sdk/` — multiplayer SDK (getting-started, architecture, WebRTC, auth, limits, troubleshooting)
- `docs/superpowers/` — design docs and plans for larger features
- `GET /docs` — live Swagger UI; `GET /openapi.yaml` (admin-only) serves the spec
