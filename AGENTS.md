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
| `--stun-servers` | `PLAYMORE_STUN_SERVERS` | stun:stun.l.google.com:19302 | Comma-separated STUN URLs for WebRTC NAT traversal |
| `--turn-servers` | `PLAYMORE_TURN_SERVERS` | "" | Comma-separated TURN URLs (e.g. turn:user:pass@host:port) |

Gin defaults to release mode unless `GIN_MODE` is set.

## Architecture

- **Backend**: Go 1.26+ with Gin. SQLite via `modernc.org/sqlite` (pure Go, no CGO).
- **Frontend**: Single `frontend/index.html` (~4500 lines) with inline CSS/JS. No build step, no framework. `frontend/js/`, `frontend/css/`, and `frontend/assets/icons/` exist but are mostly empty — keep the SPA single-file unless there's a strong reason to split.
- **Assets**: Embedded via `//go:embed all:frontend`. No external files needed at runtime.
- **Routing**: Hash-based SPA (`#store`, `#game/<id>`, `#developer/<name>`). All non-API/non-play routes fall back to `index.html`. API routes are mounted on both `/api/v1/` (canonical) and `/api/` (permanent alias for backward compat) — see `internal/server/routes.go` and the route-equivalence test in `internal/server/routes_test.go`.
- **Game files**: Served at `/play/:id/*filepath` for iframe embedding. Stored at `{dataDir}/games/{gameID}/`.
- **Uploads**: Images at `{dataDir}/uploads/`, served at `/uploads/`.
- **Multiplayer**: Complete lobby + WebRTC P2P system. Lobby matchmaking over WebSocket (`/ws`), WebRTC P2P mesh with transparent relay fallback. Client SDK: `playmore-mp.js` (embedded, served at `/playmore-mp.js`). Features: host migration, rejoin after disconnect, lobby persistence (SQLite), spectator mode, public lobby browser, lobby metadata, star topology option, unreliable data channel, connection quality (RTT/ping), adaptive throttle, bandwidth stats, keepalive + auto-reconnection, **Quick Play matchmaking** (auto-match with random players, 60s timeout). Auth: `pm_gs_` game session tokens (5-min, scoped, game-bound) + `pm_gk_` SDK keys (long-lived, per-game). CORS middleware for opaque-origin game iframes. Play sessions (`play_sessions` table) track active game sessions for analytics + `online_players` count. STUN/TURN configurable via `--stun-servers` / `--turn-servers`. Lobby state persisted to `lobbies` table, restored on startup. See `docs/sdk/` for the full SDK documentation.

## Database

SQLite with WAL mode. Schema and migrations live in `internal/storage/db.go`. Migrations run automatically on startup.

## Authentication

- **Sessions**: bcrypt passwords, 30-day session tokens in HTTP-only `session` cookies with SameSite=Lax.
- **API Keys**: Bearer tokens prefixed `pm_k_`. Some endpoints require session auth and reject API keys.
- **Game SDK Keys**: Bearer tokens prefixed `pm_gk_` (per-game, long-lived). Cannot access account endpoints (rejected by `AuthRequired`).
- **Game Session Tokens**: Bearer tokens prefixed `pm_gs_` (per-game, 5-min TTL). Used by game iframes for WebSocket auth + play sessions. Cannot access account endpoints.
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

Lightweight Go table tests live in `internal/models/` (`featured_test.go`, `upload_session_test.go`).
Run them with `go test ./...` — all other packages have no test files. There is no
frontend test setup and no CI linter; the closest built-in check is `go vet ./...`.

Manual / E2E:
1. Build and run server
2. Seed demo data: `curl -X POST http://localhost:8080/api/seed`
3. Open `http://localhost:8080` and test flows
4. Chunked-upload E2E (builds a fresh binary, provisions a verified user via SQL, exercises the upload/GC race fix): `scripts/verify-chunked-upload.sh` — needs `go, sqlite3, python3+bcrypt, curl, dd, zip, sha256sum`.

## Important Files

- `main.go` — entry point, CLI flags, `.env` loader, TLS setup
- `internal/server/server.go` — Gin router, all routes, SPA fallback, RTC config
- `internal/server/routes.go` — `mountAPIRoutes` shared by `/api/v1/` and `/api/`
- `internal/storage/db.go` — SQLite init, schema, migrations
- `internal/storage/files.go` — game file storage, ZIP extraction
- `internal/lobby/hub.go` — lobby management, host migration, rejoin, spectators, public lobbies, graceful shutdown
- `internal/lobby/persist.go` — async lobby persistence to SQLite
- `internal/lobby/protocol.go` — wire protocol (ClientMsg, ServerMsg, State)
- `internal/handlers/ws.go` — WebSocket lobby handler
- `internal/handlers/playmore-mp.js` — client SDK (WebRTC, keepalive, topology, unreliable channel)
- `internal/handlers/play_sessions.go` — play session open/heartbeat/end
- `internal/handlers/sdk_keys.go` — SDK key CRUD (pm_gk_)
- `internal/handlers/sdk_token.go` — session token mint/revoke (pm_gs_)
- `internal/handlers/lobbybrowser.go` — public lobby list endpoint
- `internal/middleware/auth.go` — auth middleware (pm_k_, pm_gk_, pm_gs_, WSQueryTokenAuth)
- `internal/middleware/cors.go` — CORS for opaque-origin game iframes
- `cmd/mp-test/main.go` — E2E multiplayer test harness
- `frontend/index.html` — entire SPA frontend
- `docs/sdk/` — multiplayer SDK documentation
- `docs/` — setup guides and API reference

## v1 Archive

`v1/index.html` is the original prototype (localStorage, no server). Not actively developed. Auto-deployed to GitHub Pages on pushes to `v1/**`.
