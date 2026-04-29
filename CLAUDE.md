# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PlayMore is a self-hosted game publishing platform for HTML5 games (Go + Gin + SQLite). Single binary deployment with embedded frontend via `go:embed`. Pure Go SQLite driver, **no CGO**.

## Commands

```bash
go build -o playmore                                      # Build
./playmore setup                                          # Interactive .env wizard
./playmore                                                # Run (reads .env, then flags/env)
./playmore --port 3000 --data /path/to                    # Custom port and data dir
./playmore --tls-cert cert.pem --tls-key key.pem          # Direct TLS
./playmore --auto-tls --domain playmore.example.com       # Let's Encrypt (needs :80 + :443)
./playmore --smtp-host ... --smtp-user ... --base-url ... # Enable email (verify / reset)
curl -X POST localhost:8080/api/seed                      # Seed demo data
docker-compose up -d                                      # Docker deployment
```

Config precedence: **CLI flag > env var > `.env` file**. Env-var fallbacks: `PLAYMORE_PORT`, `PLAYMORE_DATA`, `PLAYMORE_GOATCOUNTER`, `PLAYMORE_TLS_CERT`, `PLAYMORE_TLS_KEY`, `PLAYMORE_AUTO_TLS`, `PLAYMORE_DOMAIN`, `PLAYMORE_BASE_URL`, `PLAYMORE_SMTP_{HOST,PORT,USER,PASS,FROM}`. `.env` is loaded at startup via `loadEnvFile()` and never overrides existing env.

## Testing

No automated test suite. Test manually: build, seed demo data, exercise flows in browser. **Do not skip this** — there is no CI safety net.

## Architecture

```
main.go                    # Entry, CLI flags, .env loader, setup wizard, go:embed frontend,
                           #   SMTP health check + auto-start of protonmail-bridge on Linux
internal/
  server/server.go         # Gin router: HTTPS redirect, security headers, per-request CSP nonce,
                           #   gzip, cache-control, all routes
  handlers/                # HTTP handlers — auth, games, library, reviews, profile, developer,
                           #   feed, devlogs, comments, social, admin, admin_analytics, analytics,
                           #   achievements, notifications, settings, uploads, sanitize, seed,
                           #   apikeys, deployscript, docs (API reference HTML), avatar
  handlers/playmore-deploy.sh  # Embedded shell script served at /deploy.sh
  models/                  # DB queries — user, game, review, activity, developer, apikey
  storage/db.go            # SQLite schema + idempotent migrations (ALTER TABLE / CREATE IF NOT EXISTS)
  storage/files.go         # Game file storage, ZIP extraction with path traversal protection
  middleware/              # auth.go (session + Bearer API key), ratelimit.go (per-IP, per-endpoint),
                           #   csrf.go (Origin/Referer; bypassed for API-key auth), analytics.go
  email/email.go           # SMTP sender, health check, ProtonMail Bridge detection
frontend/index.html        # Vanilla JS SPA (single file ~262KB, inline CSS/JS)
docs/                      # SETUP.md, DEVELOPER.md (API reference), SETUP_PROTONMAIL_BRIDGE.md
v1/                        # Original single-file HTML version (archived, not actively developed)
```

## Key patterns

- **Auth (two methods)**: `middleware.AuthOptional()` first checks `Authorization: Bearer pm_k_*` (API key, validated against `api_keys` table) — invalid Bearer **rejects immediately**. Falls back to `session` HTTP-only cookie (SameSite=Lax). Use `middleware.GetUser(c)` for current user, `middleware.IsAPIKeyAuth(c)` to detect API-key requests, `middleware.IsSecure(c)` to detect HTTPS (direct TLS or `X-Forwarded-Proto: https`). `middleware.AuthRequired()` enforces presence; `handlers.RequireVerifiedEmail()` enforces verified email on write paths.
- **CSRF**: Origin/Referer validation on state-changing requests, applied **after** AuthOptional so API-key requests can skip CSRF (see `middleware/csrf.go`). The API only accepts JSON and multipart/form-data.
- **Admin**: First registered user is admin (lowest `created_at`). Admin endpoints return **404, not 403** to hide existence.
- **API keys / developer platform**: Keys prefixed `pm_k_`, stored as bcrypt-style hash + short `key_prefix` for lookup. Endpoints under `/api/api-keys`. Public deploy CLI served at `GET /deploy.sh` (rate-limited 10/min/IP). API reference rendered at `GET /docs` from `handlers/docs.go`.
- **CSP**: Per-request 16-byte nonce generated in `server.go` NoRoute handler, injected into `<style>`/`<script>` tags. `script-src-attr 'unsafe-inline'` is intentional (allows `onclick=`/inline `style=` attributes used heavily by the SPA). GoatCounter URL extends `script-src` / `connect-src` when configured.
- **Frontend**: Single HTML file, all rendering via `innerHTML` template strings. `api(path, opts)` helper wraps `fetch()`. Hash routing (`#store`, `#game/<id>`, `#developer/<name>`) via `navigate(tab)`. Always pass user input through `escapeHtml()`.
- **Game serving**: Files at `/play/<id>/*filepath` via iframe. ZIP uploads auto-extracted, entry HTML detected. WebGPU works natively. `gzip` middleware excludes `/play/` to keep Range support intact.
- **Database**: SQLite via `modernc.org/sqlite` (pure Go), WAL mode, `SetMaxOpenConns(1)`. FTS5 on games (title, description, tags) with auto-indexing triggers. Migrations in `db.go::migrate()` are idempotent — add new ones to that slice, never edit existing entries.
- **Rate limiting**: Per-IP, per-endpoint, in-memory. Cleanup goroutine started by `middleware.StartRateLimitCleanup()` in `main.go`. Applied to auth, upload, mutations, deploy script, and other sensitive endpoints in `server.go`.
- **Analytics**: Page views written asynchronously via channel; `middleware.StartAnalyticsWriter()` batches every 5s or 50 records. 90-day retention.
- **Email**: Optional. `internal/email` package; configured via `--smtp-*` flags or `PLAYMORE_SMTP_*` env. `BaseURL` (`PLAYMORE_BASE_URL`) is required for verification/reset links to work. On Linux, if SMTP host looks like a local ProtonMail Bridge, `main.go` tries `systemctl start protonmail-bridge` (system + user) before giving up. Self-signed certs are accepted for local SMTP bridges.
- **File storage**: Games at `{dataDir}/games/{gameID}/`, uploads at `{dataDir}/uploads/`, autocert cache at `{dataDir}/certs/`.
- **Secure cookies**: `Secure` flag set automatically when `IsSecure(c)` is true (direct TLS or reverse-proxy `X-Forwarded-Proto: https`).
- **HTTPS redirect**: If `X-Forwarded-Proto: http` is seen, the server 301s to https. Combined with HSTS, do not break this when adding middleware — it must run before security headers.

## Database tables

users, sessions, api_keys, games, games_fts, reviews, library, wishlist, playtime, activity, developer_pages, devlogs, comments, follows, collections, notifications, game_views, page_views, user_achievements

## Go style

- Standard `go fmt`. Handlers return JSON via `gin.H{}`. Early-return error handling. `sql.NullString` for nullable fields. Log errors with `log.Println()`.
- Module path: `github.com/yusufkaraaslan/play-more` (note: hyphenated, despite the dir being `playmore`).

## Adding routes / migrations

- New route → register in `internal/server/server.go` with appropriate middleware chain (auth, CSRF inherited, rate-limit explicit, `RequireVerifiedEmail` for writes that need it).
- New schema change → append `ALTER TABLE … ADD COLUMN …` or `CREATE … IF NOT EXISTS` to the migrations slice in `internal/storage/db.go`. Never edit `const schema` in a way that would break existing dbs.

## Reference docs

- `docs/SETUP.md` — production config (HTTPS, email, systemd)
- `docs/DEVELOPER.md` — API keys, deploy CLI, full API reference
- `docs/SETUP_PROTONMAIL_BRIDGE.md` — Proton SMTP setup
- `AGENTS.md` — longer-form agent guide (overlaps with this file)
