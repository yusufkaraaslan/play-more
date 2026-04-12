# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PlayMore is a self-hosted game publishing platform for HTML5 games (Go + Gin + SQLite). Single binary deployment with embedded frontend via `go:embed`. No CGO required.

## Commands

```bash
go build -o playmore                    # Build
./playmore                               # Run (localhost:8080)
./playmore --port 3000 --data /path/to   # Custom port and data dir
./playmore --tls-cert cert.pem --tls-key key.pem  # Direct TLS
curl -X POST localhost:8080/api/seed     # Seed demo data
docker-compose up -d                     # Docker deployment
```

All flags have `PLAYMORE_*` env var fallbacks: `PLAYMORE_PORT`, `PLAYMORE_DATA`, `PLAYMORE_GOATCOUNTER`, `PLAYMORE_TLS_CERT`, `PLAYMORE_TLS_KEY`. Flags take priority over env vars.

## Testing

No automated test suite exists. Test manually by building, seeding demo data, and exercising user flows in the browser.

## Architecture

```
main.go                    # Entry point, CLI flags, go:embed frontend
internal/
  server/server.go         # Gin router: security headers, CSP, HTTPS redirect, all routes
  handlers/                # HTTP handlers (19 files: auth, games, library, reviews, profile,
                           #   developer, feed, devlogs, comments, social, admin, admin_analytics,
                           #   analytics, achievements, notifications, settings, uploads, sanitize, seed)
  models/                  # DB queries (user, game, review, activity, developer)
  storage/db.go            # SQLite schema (~19 tables), migrations
  storage/files.go         # Game file storage, ZIP extraction with path traversal protection
  middleware/               # auth.go (session), ratelimit.go (per-IP), csrf.go, analytics.go
frontend/
  index.html               # Vanilla JS SPA (~2800 lines, inline CSS/JS)
```

## Key patterns

- **Auth**: bcrypt passwords, session tokens in `sessions` table, HTTP-only cookies with SameSite=Lax. `middleware.AuthRequired()` / `middleware.AuthOptional()`. Current user via `middleware.GetUser(c)`.
- **Admin**: first registered user is admin (lowest `created_at`). Admin endpoints return 404 (not 403) to hide existence.
- **Game serving**: files at `/play/<id>/` via iframe. ZIP uploads auto-extracted, entry file detected. WebGPU works natively.
- **Frontend**: single HTML file, all rendering via `innerHTML` template strings. `api(path, opts)` helper wraps `fetch()`. Hash routing (`#store`, `#game/<id>`, `#developer/<name>`) via `navigate(tab)`. Use `escapeHtml()` for user input.
- **Database**: SQLite with WAL mode, pure Go driver (`modernc.org/sqlite`). FTS5 on games (title, description, tags) with auto-indexing triggers.
- **Rate limiting**: per-IP, per-endpoint, in-memory. Applied to auth, upload, and sensitive endpoints.
- **CSRF**: Origin/Referer validation on state-changing requests. API only accepts JSON and multipart/form-data.
- **Analytics**: page views tracked asynchronously via channel, batch inserts every 5s or 50 records, 90-day retention.
- **File storage**: games at `{dataDir}/games/{gameID}/`, uploads at `{dataDir}/uploads/`.
- **Secure cookies**: `Secure` flag set automatically when `X-Forwarded-Proto: https` is detected (reverse proxy) or TLS is active.

## Database tables

users, sessions, games, games_fts, reviews, library, wishlist, playtime, activity, developer_pages, devlogs, comments, follows, collections, notifications, game_views, page_views, user_achievements

## Go style

- Standard `go fmt`. Handlers return JSON via `gin.H{}`. Early-return error handling. `sql.NullString` for nullable fields. Log errors with `log.Println()`.

## v1

Original single-file version archived in `v1/`. Not actively developed.
