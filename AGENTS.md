# AGENTS.md

This file provides comprehensive guidance for AI coding agents working with the PlayMore codebase.

## Project Overview

PlayMore is a **self-hosted game publishing platform** for HTML5 games — think Steam or itch.io but you own the server. It is a full-stack Go web application with a vanilla JavaScript SPA frontend.

## Technology Stack

- **Backend**: Go 1.26+ with Gin web framework
- **Database**: SQLite (pure Go driver via `modernc.org/sqlite`, no CGO required)
- **Frontend**: Vanilla JavaScript SPA (~2800 lines, single HTML file with inline CSS/JS, no frameworks)
- **Authentication**: bcrypt password hashing + session tokens stored in HTTP-only cookies
- **Deployment**: Single binary with embedded frontend assets (`go:embed`)

## Project Structure

```
/mnt/1ece809a-2821-4f10-aecb-fcdf34760c0b/Git/playmore/
├── main.go                      # Entry point, CLI flags, go:embed frontend
├── go.mod, go.sum              # Go module dependencies
├── Dockerfile                   # Multi-stage Docker build
├── docker-compose.yml           # Docker Compose configuration
├── frontend/
│   ├── index.html              # Single-page application (inline CSS/JS ~2800 lines)
│   ├── css/                    # CSS directory (empty - styles inline)
│   ├── js/                     # JS directory (empty - scripts inline)
│   ├── assets/                 # Static assets
│   └── .well-known/            # Security files (security.txt)
├── internal/
│   ├── server/
│   │   └── server.go           # Gin router setup (HTTPS redirect, security headers, CSP, routes)
│   ├── handlers/
│   │   ├── auth.go             # Register, login, logout, session
│   │   ├── games.go            # List, get, upload, update, delete games
│   │   ├── library.go          # Library and wishlist management
│   │   ├── reviews.go          # Create, list, delete reviews
│   │   ├── profile.go          # User profiles and activity
│   │   ├── developer.go        # Developer pages and stats
│   │   ├── feed.go             # Aggregated activity feed
│   │   ├── devlogs.go          # Developer blog posts
│   │   ├── comments.go         # Comments on devlogs
│   │   ├── social.go           # Follow/unfollow, collections
│   │   ├── admin.go            # Admin panel (moderation)
│   │   ├── admin_analytics.go  # Site-wide analytics
│   │   ├── analytics.go        # Per-game analytics tracking
│   │   ├── achievements.go     # User achievements system
│   │   ├── settings.go         # Account settings, password change
│   │   ├── uploads.go          # Image upload handler
│   │   ├── notifications.go    # User notifications
│   │   ├── sanitize.go         # HTML sanitization utilities
│   │   └── seed.go             # Demo data seeding
│   ├── models/
│   │   ├── user.go             # User CRUD, sessions, bcrypt
│   │   ├── game.go             # Game CRUD, slug generation, FTS search
│   │   ├── review.go           # Review CRUD, rating aggregation
│   │   ├── activity.go         # Activity logging for feeds
│   │   └── developer.go        # Developer page CRUD
│   ├── storage/
│   │   ├── db.go               # SQLite connection, schema, migrations (19 tables)
│   │   └── files.go            # Game file storage, ZIP extraction
│   └── middleware/
│       ├── auth.go             # Session authentication (required/optional)
│       ├── ratelimit.go        # Per-IP rate limiting
│       ├── csrf.go             # CSRF protection via Origin/Referer headers
│       └── analytics.go        # Page view tracking, session management
└── v1/
    └── index.html              # Original single-file version (archived, deployed to GitHub Pages)
```

## Build and Run Commands

```bash
# Build the binary
go build -o playmore

# Run with defaults (port 8080, data directory "data")
./playmore

# Run with custom options
./playmore --port 3000 --data /path/to/data

# Run with GoatCounter analytics
./playmore --goatcounter https://mysite.goatcounter.com

# Seed demo data (4 games with reviews)
curl -X POST http://localhost:8080/api/seed

# Docker deployment
docker-compose up -d
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | 8080 | Server port |
| `--data` | data | Data directory path |
| `--goatcounter` | "" | GoatCounter URL for analytics |

## Database Schema

SQLite with WAL mode enabled. Key tables:

| Table | Purpose |
|-------|---------|
| `users` | User accounts, passwords (bcrypted), profiles |
| `sessions` | Session tokens with 30-day expiry |
| `games` | Game metadata, file paths, slugs |
| `reviews` | Star ratings (1-5) and text reviews |
| `library` | User's owned games |
| `wishlist` | User's wishlisted games |
| `playtime` | Play time tracking per user/game |
| `activity` | Feed events (uploads, follows, etc.) |
| `developer_pages` | Customizable developer storefronts |
| `devlogs` | Blog posts tied to games |
| `comments` | Comments on devlogs |
| `follows` | Follow relationships |
| `collections` | User-created game collections |
| `notifications` | User notifications |
| `game_views` | Game page view tracking |
| `page_views` | Site-wide page view analytics |
| `user_achievements` | Gamification achievements |

Full-text search (FTS5) enabled on games via `games_fts` virtual table with triggers for automatic indexing.

## API Structure

All API routes are prefixed with `/api/`:

```
# Auth
POST   /api/auth/register          # Rate limited: 5/hour
POST   /api/auth/login             # Rate limited: 10/5min
POST   /api/auth/logout
GET    /api/auth/me

# Games
GET    /api/games                  # Query: genre, search, sort, page, limit
GET    /api/games/:id
POST   /api/games                  # Auth required, rate limited: 10/hour
PUT    /api/games/:id              # Auth required
DELETE /api/games/:id              # Auth required

# Reviews
GET    /api/games/:id/reviews
POST   /api/games/:id/reviews     # Auth required, rate limited: 20/hour
DELETE /api/reviews/:id            # Auth required

# Library
GET    /api/library               # Auth required
POST   /api/library/:game_id      # Auth required
DELETE /api/library/:game_id      # Auth required

# Wishlist
GET    /api/wishlist              # Auth required
POST   /api/wishlist/:game_id     # Auth required
DELETE /api/wishlist/:game_id     # Auth required

# Profile
GET    /api/profile/:username
PUT    /api/profile                # Auth required, rate limited: 10/5min
GET    /api/activity               # Auth required
POST   /api/playtime               # Auth required

# Settings
DELETE /api/settings/account       # Auth required, rate limited: 3/hour
PUT    /api/settings/password      # Auth required, rate limited: 5/hour

# Developer pages
GET    /api/developer/:username
PUT    /api/developer              # Auth required, rate limited: 10/5min
GET    /api/developer/:username/games

# Achievements
GET    /api/achievements/:username
POST   /api/achievements/check    # Auth required

# Analytics
POST   /api/games/:id/view        # Track game view
POST   /api/analytics/client      # Track client info (screen, WebGPU)
GET    /api/games/:id/analytics   # Auth required (developer only)

# Notifications
GET    /api/notifications          # Auth required
POST   /api/notifications/read    # Auth required

# Feed
GET    /api/feed                   # Auth required

# Devlogs
GET    /api/games/:id/devlogs
POST   /api/games/:id/devlogs     # Auth required
DELETE /api/devlogs/:id            # Auth required

# Comments
GET    /api/devlogs/:id/comments
POST   /api/devlogs/:id/comments  # Auth required
DELETE /api/comments/:id           # Auth required

# Follows
POST   /api/follow/:username      # Auth required, rate limited: 30/hour
DELETE /api/follow/:username      # Auth required, rate limited: 30/hour
GET    /api/following              # Auth required
GET    /api/followers/:username

# Collections
GET    /api/collections            # Auth required
POST   /api/collections            # Auth required
DELETE /api/collections/:id        # Auth required
POST   /api/collections/:id/games # Auth required
DELETE /api/collections/:id/games/:game_id  # Auth required

# Admin (admin only)
GET    /api/admin/stats
GET    /api/admin/users
DELETE /api/admin/users/:id        # Rate limited: 10/hour
GET    /api/admin/games
DELETE /api/admin/games/:id        # Rate limited: 10/hour
PUT    /api/admin/games/:id/publish
GET    /api/admin/analytics

# Uploads
POST   /api/upload/image           # Auth required
POST   /api/seed                   # No auth (creates demo data)
```

Game files are served at `/play/:id/*filepath` for iframe embedding.

Static uploads are served at `/uploads/`.

SPA fallback: All non-API, non-play routes serve `frontend/index.html`.

## Code Style Guidelines

### Go
- Standard Go formatting (`go fmt`)
- Handlers return JSON via `gin.H{}`
- Error handling: Return early pattern
- Log errors with `log.Println()` if needed
- Use `sql.NullString` for nullable fields
- Get current user via `middleware.GetUser(c)`

### Frontend
- Single HTML file with inline CSS/JS
- All rendering via template strings and `innerHTML`
- API client uses `api(path, opts)` helper that wraps `fetch()` with JSON handling
- Navigation: Hash-based routing (`#store`, `#game/<id>`, `#developer/<name>`)
- Use `escapeHtml()` for user input to prevent XSS

## Key Implementation Details

### Authentication Flow
1. Passwords hashed with bcrypt (default cost)
2. Sessions stored in `sessions` table with 30-day expiry
3. Session token passed via HTTP-only cookie named "session"
4. `middleware.AuthOptional()` loads user if session exists
5. `middleware.AuthRequired()` rejects unauthenticated requests
6. First registered user becomes admin (lowest `created_at`)

### CSRF Protection
- State-changing requests validated via Origin/Referer headers
- API only accepts JSON and multipart/form-data Content-Type
- Cross-origin form submissions are blocked

### Game Upload Process
1. Validate title and genre
2. Create game record in DB
3. Accept `.html` or `.zip` files
4. ZIP files auto-extracted, entry file detected (looks for `index.html`)
5. Optional cover image saved alongside game files
6. Multiple screenshots supported
7. User marked as developer (`is_developer = 1`)
8. Activity logged for feeds

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

### Analytics
- Page views tracked asynchronously via channel
- Batch inserts every 5 seconds or 50 records
- Session tracking with 30-minute timeout
- Old data cleanup runs hourly (keeps 90 days)
- Client info (screen resolution, WebGPU) tracked via POST endpoint

### Security Headers
- Content-Security-Policy with configurable GoatCounter domain
- X-Content-Type-Options: nosniff
- X-Frame-Options: SAMEORIGIN
- Referrer-Policy: strict-origin-when-cross-origin
- Strict-Transport-Security (when HTTPS detected)
- Permissions-Policy restricting device features

## Testing

There is no automated test suite. Manual testing is performed by:

1. Building and running the server
2. Seeding demo data: `curl -X POST http://localhost:8080/api/seed`
3. Opening `http://localhost:8080` in a browser
4. Testing user flows: register, upload game, review, follow, etc.

## Security Considerations

- **Passwords**: Bcrypt hashed, never stored plaintext
- **Sessions**: 30-day expiry, stored server-side, HTTP-only cookies with SameSite=Lax
- **Rate limiting**: Applied to auth, upload, and sensitive endpoints
- **File uploads**: Extension checks, path traversal protection in ZIP extraction
- **SQL**: Parameterized queries throughout (no SQL injection risk)
- **XSS**: Frontend uses `escapeHtml()` and `textContent` for user input
- **CSRF**: Origin/Referer validation on state-changing requests
- **Admin**: First registered user is admin; admin endpoints return 404 (not 403) to hide existence

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
