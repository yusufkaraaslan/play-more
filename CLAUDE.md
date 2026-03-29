# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PlayMore is a **single-file web application** (`index.html`, ~2960 lines) — a self-hosted game publishing platform UI modeled after Steam. Self-contained HTML/CSS/JS with no build system or external JS dependencies.

## Running

Open `index.html` directly in a browser (`file://` protocol works). No server required.

## Architecture

Everything lives in one `index.html` with three inline sections:

### CSS (~1350 lines)
- Custom properties in `:root` for theming (`--bg-primary`, `--accent`, `--card-bg`, etc.)
- Steam-like dark theme: `#1b2838` background, `#66c0f4` accent blue
- Desktop-first, responsive down to 1366x768
- Overlay animations use `pointer-events`/`opacity`/`transform` (not `display` toggling)

### HTML (~530 lines)
Tab-based SPA with 7 sections:
- **Home** (Store): hero banner, offers, paginated game listings with search/filter/sort
- **Game Detail**: shared template populated by `showGameDetail(gameId)` — media carousel, reviews (with write-review form), purchase box, community hub, system requirements
- **Library**: grid of owned games
- **Profile**: dynamic stats, activity feed, editable username
- **Upload**: drag-drop file upload with IndexedDB storage
- **Dashboard**: creator tools — edit/delete uploaded games with stats

Navigation via `data-tab` attributes and `switchTab()`.

### JavaScript (~1080 lines)

**Data layer**:
- `gamesData` — game catalog object keyed by slug. Hardcoded games + uploaded games merged at startup
- `reviewsData` — static review arrays. User reviews stored separately and merged at render
- **IndexedDB** (`playmore_db` v1): `game_files` store holds uploaded game ArrayBuffers (keyPath: `id`)
- **localStorage keys**:
  - `playmore_library` — array of game IDs
  - `playmore_uploaded_games` — array of uploaded game metadata (same shape as gamesData entries + `isUploaded: true`)
  - `playmore_reviews` — `{ gameId: Review[] }` user-written reviews
  - `playmore_playtime` — `{ gameId: totalSeconds }` per-game playtime
  - `playmore_activity` — recent actions array (max 50 entries)
  - `playmore_username` — editable profile name

**Key flows**:
- **Upload**: `handleFiles()` reads via FileReader → `submitGameUpload()` saves to IndexedDB + localStorage, generates canvas cover, injects into `gamesData`
- **Play**: `playGame(gameId)` — XOX Classic uses inline blob HTML, uploaded games load from IndexedDB, others show demo placeholder. Tracks session start time.
- **Close**: `closeGamePlayer()` records elapsed playtime to localStorage, logs activity
- **Reviews**: `renderReviews()` merges static + user reviews, dynamically computes rating percentage
- **Profile**: `renderProfile()` computes all stats from real localStorage data
- **Pagination**: `displayGamesPage()` shows `GAMES_PER_PAGE` (10) items with "Load More" button

**Important patterns**:
- `gameRowHTML(g)` is the shared template for game list rows — used by store, filters, uploaded games, dashboard
- `logActivity(type, gameId, detail)` — called from play, library add, upload, review
- `editingGameId` module var enables edit mode in the upload form (reuses same form for create/edit)
- Genre navigation: `showHome()` resets filters. To navigate with genre pre-selected, use `switchTab('home')` + set dropdown directly.

**External dependencies** (images only):
- Unsplash for game covers/screenshots
- DiceBear API for avatars
- YouTube embed for video trailers
