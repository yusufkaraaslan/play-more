# AGENTS.md — PlayMore

Self-hosted HTML5 game publishing platform. Full-stack Go + vanilla JS SPA, single binary with embedded frontend.

## Build & Run

```bash
go build -o playmore
./playmore                    # defaults: port 8080, data dir "data"
./playmore setup              # interactive production wizard (creates .env)
```

Seed demo data: `curl -X POST http://localhost:8080/api/seed`

Docker: `docker-compose up -d`

## CLI Flags / Environment Variables

Flags take priority over env vars (`PLAYMORE_*`). All env vars are optional.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--port` | `PLAYMORE_PORT` | 8080 | Server port |
| `--data` | `PLAYMORE_DATA` | data | Data directory |
| `--goatcounter` | `PLAYMORE_GOATCOUNTER` | "" | GoatCounter analytics URL |
| `--base-url` | `PLAYMORE_BASE_URL` | "" | Public URL (for emails, redirects) |
| `--games-domain` | `PLAYMORE_GAMES_DOMAIN` | "" | Separate domain for game files (isolation) |
| `--tls-cert` | `PLAYMORE_TLS_CERT` | "" | TLS certificate path |
| `--tls-key` | `PLAYMORE_TLS_KEY` | "" | TLS private key path |
| `--auto-tls` | `PLAYMORE_AUTO_TLS` | false | Let's Encrypt auto-TLS |
| `--domain` | `PLAYMORE_DOMAIN` | "" | Domain for auto-TLS |
| `--smtp-host` | `PLAYMORE_SMTP_HOST` | "" | SMTP server |
| `--smtp-port` | `PLAYMORE_SMTP_PORT` | 587 | SMTP port |
| `--smtp-user` | `PLAYMORE_SMTP_USER` | "" | SMTP username |
| `--smtp-pass` | `PLAYMORE_SMTP_PASS` | "" | SMTP password |
| `--smtp-from` | `PLAYMORE_SMTP_FROM` | "" | From address |
| `--trusted-proxies` | `PLAYMORE_TRUSTED_PROXIES` | "" | Comma-separated trusted proxy CIDRs |
| `--behind-tls-proxy` | `PLAYMORE_BEHIND_TLS_PROXY` | false | Force Secure cookie flag |

Gin defaults to release mode unless `GIN_MODE` is set.

## Architecture

- **Backend**: Go 1.26+ with Gin. SQLite via `modernc.org/sqlite` (pure Go, no CGO).
- **Frontend**: Single `frontend/index.html` (~3500 lines) with inline CSS/JS. No build step, no framework.
- **Assets**: Embedded via `//go:embed all:frontend`. No external files needed at runtime.
- **Routing**: Hash-based SPA (`#store`, `#game/<id>`, `#developer/<name>`). All non-API/non-play routes fall back to `index.html`.
- **Game files**: Served at `/play/:id/*filepath` for iframe embedding. Stored at `{dataDir}/games/{gameID}/`.
- **Uploads**: Images at `{dataDir}/uploads/`, served at `/uploads/`.

## Database

SQLite with WAL mode. Schema and migrations live in `internal/storage/db.go`. Migrations run automatically on startup.

## Authentication

- **Sessions**: bcrypt passwords, 30-day session tokens in HTTP-only `session` cookies with SameSite=Lax.
- **API Keys**: Bearer tokens prefixed `pm_k_`. Some endpoints require session auth and reject API keys.
- **Email verification**: Required for uploading games, writing reviews, creating devlogs/comments.
- **Admin**: First registered user becomes admin.

## Code Conventions

### Go
- Standard `go fmt`. Handlers return JSON via `gin.H{}`.
- Get current user: `middleware.GetUser(c)`. Check API key auth: `middleware.IsAPIKeyAuth(c)`.
- Use `sql.NullString` for nullable fields. Return early for errors.
- Rate limiting: `middleware.RateLimit(max, windowSeconds)`.

### Frontend
- All rendering via template strings + `innerHTML`. API client: `api(path, opts)` helper wrapping `fetch()`.
- Escape user input with `escapeHtml()` before injecting into HTML.
- Dark/light theme via `data-theme` attribute on `<html>`.

## Testing

No automated test suite. Manual testing:
1. Build and run server
2. Seed demo data: `curl -X POST http://localhost:8080/api/seed`
3. Open `http://localhost:8080` and test flows

## Important Files

- `main.go` — entry point, CLI flags, `.env` loader, TLS setup
- `internal/server/server.go` — Gin router, all routes, SPA fallback
- `internal/storage/db.go` — SQLite init, schema, migrations
- `internal/storage/files.go` — game file storage, ZIP extraction
- `frontend/index.html` — entire SPA frontend
- `docs/` — setup guides and API reference

## v1 Archive

`v1/index.html` is the original prototype (localStorage, no server). Not actively developed. Auto-deployed to GitHub Pages on pushes to `v1/**`.
