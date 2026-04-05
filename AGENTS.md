# AGENTS.md

This file provides guidance for AI coding agents working with the PlayMore codebase.

## Project Overview

PlayMore is a **self-hosted game publishing platform** for HTML5 games — think Steam or itch.io but you own the server. It is a full-stack Go web application with a vanilla JavaScript SPA frontend.

## Technology Stack

- **Backend**: Go 1.26+ with Gin web framework
- **Database**: SQLite (pure Go driver via `modernc.org/sqlite`, no CGO required)
- **Frontend**: Vanilla JavaScript SPA (single HTML file, ~2000 lines, no frameworks)
- **Authentication**: bcrypt password hashing + session tokens stored in cookies
- **Deployment**: Single binary with embedded frontend assets (`go:embed`)

## Project Structure

```
/mnt/1ece809a-2821-4f10-aecb-fcdf34760c0b/Git/playmore/
├── main.go                      # Entry point, CLI flags, go:embed frontend
├── go.mod, go.sum              # Go module dependencies
├── Dockerfile                   # Multi-stage Docker build
├── docker-compose.yml           # Docker Compose configuration
├── frontend/
│   └── index.html              # Single-page application (inline CSS/JS)
├── internal/
│   ├── server/
│   │   └── server.go           # Gin router setup (40+ endpoints)
│   ├── handlers/
│   │   ├── auth.go             # Register, login, logout, session
│   │   ├── games.go            # List, get, upload, update, delete games
│   │   ├── library.go          # Library and wishlist management
│   │   ├── reviews.go          # Create, list, delete reviews
│   │   ├── profile.go          # User profiles and activity
│   │   ├── developer.go        # Developer pages and stats
│   │   ├── feed.go             # Aggregated activity feed
│   │   ├── devlogs.go          # Developer blog posts
│   │   ├── social.go           # Follow/unfollow, collections
│   │   ├── admin.go            # Admin panel (moderation)
│   │   ├── settings.go         # Account settings, password change
│   │   ├── uploads.go          # Image upload handler
│   │   ├── notifications.go    # User notifications
│   │   └── seed.go             # Demo data seeding
│   ├── models/
│   │   ├── user.go             # User CRUD, sessions, bcrypt
│   │   ├── game.go             # Game CRUD, slug generation, FTS search
│   │   ├── review.go           # Review CRUD, rating aggregation
│   │   ├── activity.go         # Activity logging for feeds
│   │   └── developer.go        # Developer page CRUD
│   ├── storage/
│   │   ├── db.go               # SQLite connection, schema, migrations (13 tables)
│   │   └── files.go            # Game file storage, ZIP extraction
│   └── middleware/
│       ├── auth.go             # Session authentication (required/optional)
│       └── ratelimit.go        # Per-IP rate limiting
└── v1/
    └── index.html              # Original single-file version (archived)
```

## Build and Run Commands

```bash
# Build the binary
go build -o playmore

# Run with defaults (port 8080, data directory "data")
./playmore

# Run with custom options
./playmore --port 3000 --data /path/to/data

# Seed demo data (4 games with reviews)
curl -X POST http://localhost:8080/api/seed

# Docker deployment
docker-compose up -d
```

## Database Schema

SQLite with WAL mode enabled. Key tables:

| Table | Purpose |
|-------|---------|
| `users` | User accounts, passwords (bcrypted), profiles |
| `sessions` | Session tokens with expiry |
| `games` | Game metadata, file paths, slugs |
| `reviews` | Star ratings (1-5) and text reviews |
| `library` | User's owned games |
| `wishlist` | User's wishlisted games |
| `playtime` | Play time tracking per user/game |
| `activity` | Feed events (uploads, follows, etc.) |
| `developer_pages` | Customizable developer storefronts |
| `devlogs` | Blog posts tied to games |
| `follows` | Follow relationships |
| `collections` | User-created game collections |
| `notifications` | User notifications |

Full-text search (FTS5) enabled on games via `games_fts` virtual table with triggers for automatic indexing.

## API Structure

All API routes are prefixed with `/api/`:

```
POST   /api/auth/register          # Rate limited: 5/hour
POST   /api/auth/login             # Rate limited: 10/5min
POST   /api/auth/logout
GET    /api/auth/me

GET    /api/games                  # Query: genre, search, sort, page, limit
GET    /api/games/:id
POST   /api/games                  # Auth required, rate limited: 10/hour
PUT    /api/games/:id              # Auth required
DELETE /api/games/:id              # Auth required

GET    /api/games/:id/reviews
POST   /api/games/:id/reviews     # Auth required, rate limited: 20/hour
DELETE /api/reviews/:id            # Auth required

GET    /api/library               # Auth required
POST   /api/library/:game_id      # Auth required
DELETE /api/library/:game_id      # Auth required

GET    /api/wishlist              # Auth required
POST   /api/wishlist/:game_id     # Auth required
DELETE /api/wishlist/:game_id     # Auth required

GET    /api/profile/:username
PUT    /api/profile                # Auth required
GET    /api/activity               # Auth required
POST   /api/playtime               # Auth required

GET    /api/developer/:username
PUT    /api/developer              # Auth required
GET    /api/developer/:username/games

GET    /api/notifications          # Auth required
POST   /api/notifications/read    # Auth required

GET    /api/feed                   # Auth required
GET    /api/games/:id/devlogs
POST   /api/games/:id/devlogs     # Auth required
DELETE /api/devlogs/:id            # Auth required

POST   /api/follow/:username      # Auth required
DELETE /api/follow/:username      # Auth required
GET    /api/following              # Auth required
GET    /api/followers/:username

GET    /api/collections            # Auth required
POST   /api/collections            # Auth required
DELETE /api/collections/:id        # Auth required
POST   /api/collections/:id/games # Auth required
DELETE /api/collections/:id/games/:game_id  # Auth required

DELETE /api/settings/account       # Auth required
PUT    /api/settings/password      # Auth required

GET    /api/admin/stats            # Admin only
GET    /api/admin/users            # Admin only
DELETE /api/admin/users/:id        # Admin only
GET    /api/admin/games            # Admin only
DELETE /api/admin/games/:id        # Admin only
PUT    /api/admin/games/:id/publish # Admin only

POST   /api/upload/image           # Auth required
POST   /api/seed                   # No auth (creates demo data)
```

Game files are served at `/play/:id/*filepath` for iframe embedding.

Static uploads are served at `/uploads/`.

SPA fallback: All non-API, non-play routes serve `frontend/index.html`.

## Code Style Guidelines

- **Go**: Standard Go formatting (`go fmt`). Handlers return JSON via `gin.H{}`.
- **Error handling**: Return early pattern. Log errors with `log.Println()` if needed.
- **Models**: Database queries live in `internal/models/`. Use `sql.NullString` for nullable fields.
- **Handlers**: HTTP handlers in `internal/handlers/`. Get current user via `middleware.GetUser(c)`.
- **Frontend**: Single HTML file with inline CSS/JS. All rendering via template strings and `innerHTML`.
- **API client**: Frontend uses `api(path, opts)` helper that wraps `fetch()` with JSON handling.
- **Navigation**: Hash-based routing (`#store`, `#game/<id>`, `#developer/<name>`).

## Key Implementation Details

### Authentication Flow

1. Passwords hashed with bcrypt (default cost)
2. Sessions stored in `sessions` table with 30-day expiry
3. Session token passed via HTTP-only cookie named "session"
4. `middleware.AuthOptional()` loads user if session exists
5. `middleware.AuthRequired()` rejects unauthenticated requests
6. First registered user becomes admin (lowest `created_at`)

### Game Upload Process

1. Validate title and genre
2. Create game record in DB
3. Accept `.html` or `.zip` files
4. ZIP files auto-extracted, entry file detected (looks for `index.html`)
5. Optional cover image saved alongside game files
6. User marked as developer (`is_developer = 1`)
7. Activity logged for feeds

### File Storage

- Game files stored at `{dataDir}/games/{gameID}/`
- Uploads stored at `{dataDir}/uploads/`
- Cover images served via `/play/{id}/cover.ext`
- Uploaded images served via `/uploads/`
- ZIP extraction includes path traversal protection

### Rate Limiting

- Per-IP, per-endpoint tracking in memory
- Configurable max requests and window per endpoint
- Cleanup goroutine removes stale entries every 5 minutes
- Returns HTTP 429 when limit exceeded

### Search

- Full-text search via SQLite FTS5 on `title`, `description`, `tags`
- Automatic indexing via triggers on INSERT/UPDATE/DELETE
- Falls back to LIKE queries for partial matches

## Testing

There is no automated test suite. Manual testing is performed by:

1. Building and running the server
2. Seeding demo data: `curl -X POST http://localhost:8080/api/seed`
3. Opening `http://localhost:8080` in a browser
4. Testing user flows: register, upload game, review, follow, etc.

## Security Considerations

- **Passwords**: Bcrypt hashed, never stored plaintext
- **Sessions**: 30-day expiry, stored server-side, HTTP-only cookies
- **Rate limiting**: Applied to auth and upload endpoints
- **File uploads**: Extension checks, path traversal protection in ZIP extraction
- **SQL**: Parameterized queries throughout (no SQL injection risk)
- **XSS**: Frontend uses `textContent` for user input where possible
- **CSRF**: Not explicitly protected (stateless design, no CSRF tokens)

## Deployment

### Single Binary

The frontend is embedded into the binary via `go:embed`:

```go
//go:embed all:frontend
var frontendFS embed.FS
```

No external assets needed at runtime.

### Docker

Multi-stage Dockerfile:
1. Build stage: `golang:1.26-alpine` builds the binary
2. Run stage: `alpine:3.19` runs the binary

Data directory `/app/data` is exposed as a volume.

### GitHub Pages

The `v1/` directory (original single-file version) is automatically deployed to GitHub Pages via `.github/workflows/pages.yml` when changes are pushed to `v1/**`.

## v1 Archive

The `v1/index.html` file contains the original prototype — a completely self-contained HTML/CSS/JS app with `localStorage` persistence and no server. It is not actively developed but serves as a reference/demo.
