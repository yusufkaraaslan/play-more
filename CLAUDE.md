# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PlayMore is a self-hosted game publishing platform (Go + Gin + SQLite). Single binary deployment with embedded frontend.

## Commands

```bash
go build -o playmore          # Build
./playmore                     # Run (localhost:8080)
./playmore --port 3000         # Custom port
curl -X POST localhost:8080/api/seed  # Seed demo data
```

## Architecture

```
main.go                    # Entry point, go:embed frontend
internal/
  server/server.go         # Gin routes (40+ endpoints)
  handlers/                # HTTP handlers (auth, games, library, reviews, profile, developer, feed, devlogs, social, admin, settings, seed)
  models/                  # DB queries (user, game, review, activity, developer)
  storage/db.go            # SQLite schema (13 tables), migrations
  storage/files.go         # Game file storage, ZIP extraction
  middleware/auth.go        # Session auth
frontend/
  index.html               # SPA (vanilla JS, ~1700 lines)
```

## Key patterns

- **Auth**: bcrypt passwords, session tokens in `sessions` table, cookie-based
- **Game serving**: files at `/play/<id>/` — WebGPU works natively (no sandbox hacks)
- **Frontend**: single HTML file with inline CSS/JS, all rendering via `innerHTML` template strings
- **API calls**: `api(path, opts)` helper wraps `fetch()` with JSON handling
- **Navigation**: hash routing (`#store`, `#game/<id>`, `#developer/<name>`) with `navigate(tab)` function
- **Admin**: first registered user is admin (checked by `created_at` order)
- **Database**: SQLite with WAL mode, single connection, pure Go driver (no CGO)

## Database tables

users, sessions, games, reviews, library, wishlist, playtime, activity, developer_pages, devlogs, follows, collections

## v1

Original single-file version archived in `v1/`. Not actively developed.
