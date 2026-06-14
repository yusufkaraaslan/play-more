# Store Featured Hero + Curated Homepage — Design

**Date:** 2026-06-15
**Status:** Approved (design); implementation pending
**Scope:** PlayMore store landing page (`#store`)

## Goal

Turn the flat store grid into a Steam-front-page-style curated homepage. The
centerpiece is a **featured hero** at the top. Themed rows follow underneath.

Build order is explicit:

- **Phase 1 (build now):** the featured hero + filmstrip carousel.
- **Phase 2 (then add):** themed horizontal rows + "Browse all".

This document fully specifies Phase 1 and captures Phase 2 at end-state level so
the structure is coherent. Phase 2 is **not** built until Phase 1 ships.

## Current state

`#store-section` (frontend/index.html ~line 506) renders, top to bottom:

1. `#recently-played` strip (hidden unless logged in with history)
2. `.filter-bar` — search input + genre `<select>` + sort `<select>`
3. `.game-grid#store-grid` — flat grid of `gameCardHTML()` cards
4. `.load-more#store-load-more`

`loadStore()` fetches `/api/games?...` and fills the grid. No hero, no curation.

Games already carry the data a hero needs (`internal/models/game.go`):
`title`, `slug`, `developer_name`, `genre`, `tags`, `avg_rating`, `play_count`,
`screenshots []string`, `header_image`, `theme_color`, `description`,
`is_webgpu`, `cover_path`.

Constraint: PlayMore is **free-to-play only** (payments + old store hero were
removed in recent commits). No price or discount UI anywhere in this design.

## Phase 1 — Featured Hero

### Layout

Steam-faithful split with a filmstrip selector (chosen over plain dots):

```
┌─ Featured & Recommended ─────────────────────────────────┐
│ ┌───────────────────────────┐  ┌──────────────────────┐ │
│ │ ● LIVE                     │  │ Neon Drift           │ │
│ │                            │  │ ★ Very Positive ·312 │ │
│ │   large featured capsule   │  │ ┌────┐┌────┐         │ │
│ │   (header image, 16:9-ish) │  │ │ ss ││ ss │  2×2    │ │
│ │                            │  │ ├────┤├────┤  shots  │ │
│ │              NEON DRIFT    │  │ │ ss ││ ss │         │ │
│ │                            │  │ by Volt Studio       │ │
│ │                            │  │ [Racing][WebGPU]     │ │
│ │                            │  │ [ ▶ PLAY NOW ]       │ │
│ └───────────────────────────┘  └──────────────────────┘ │
│ ┌────┐┌────┐┌────┐┌────┐┌────┐┌────┐  ← filmstrip       │
│ │ ●  ││    ││    ││    ││    ││    │    (active bordered) │
│ └────┘└────┘└────┘└────┘└────┘└────┘                     │
└──────────────────────────────────────────────────────────┘
```

- **Left capsule (~⅔ width):** featured game's `header_image` (fallback:
  `cover_path`, then the generated SVG used by `gameCardHTML`). Overlays: a
  **`● LIVE`** badge (top-left) signaling instantly-playable, and the game
  **title** (bottom-left). Background tint may use `theme_color`.
- **Right info rail (~⅓ width):**
  - Title
  - Rating summary: `★ <label> · <count> ratings` where label derives from
    `avg_rating` (e.g. Very Positive / Positive / Mixed). If `rating_count == 0`
    → "No ratings yet".
  - **2×2 screenshot grid** from `screenshots[0..3]`.
  - Developer name (`by <developer_name>`).
  - Up to ~3 tag/genre chips; `WebGPU` chip when `is_webgpu`.
  - **`▶ PLAY NOW`** primary button.
  - **No price element. No "recommended because you played…".**
- **Filmstrip (full width, below):** one thumbnail per featured game (the 6
  slots). Active slot has the accent border. Clicking a thumb switches the
  hero to that game. Replaces carousel dots.

### Rail fallback (no screenshots)

When `screenshots` is empty (fresh upload), **collapse the 2×2 grid** and let
the **`description`** fill that vertical space (clamped to fit, ~3–4 lines).
Everything else (title, dev, tags, Play Now) stays. Rating line shows "No
ratings yet". Same outer layout — never a hole where screenshots would be.

### Featured selection — admin pins + auto-fill

The hero shows up to **6** games, assembled server-side:

1. **Pinned games** first, in admin-defined order (`featured_rank ASC`).
2. **Auto-fill** the remaining slots, **excluding already-pinned games**, using
   a **blend of trending + newest**, alternating:
   - odd auto-slots → **trending**: most `game_views` in the **last 7 days**
   - even auto-slots → **newest**: most recently published
   - dedupe so a game never appears twice; if one source runs dry, fill from the
     other.
3. Only **published** games are eligible (pinned or auto).

Edge cases:
- Fewer than 6 eligible games → show what exists (filmstrip shrinks).
- Zero eligible games → **hide the hero entirely**, fall back to current grid.

### Behavior

- **Auto-advance** every ~6s to the next slot.
- **Pause on hover** and on keyboard **focus** within the hero.
- Respect **`prefers-reduced-motion`** → disable auto-advance (manual only).
- Filmstrip thumbs and Play Now are keyboard-focusable; switching is operable
  by keyboard (consistent with the nav-tab accessibility already in the SPA).

### Backend — Phase 1

**Schema migration** (append to the idempotent slice in
`internal/storage/db.go::migrate()`, never edit existing entries):

```sql
ALTER TABLE games ADD COLUMN featured_rank INTEGER DEFAULT 0;
-- 0 = not pinned; >0 = pinned, ascending = display order
CREATE INDEX IF NOT EXISTS idx_games_featured_rank ON games(featured_rank);
```

**Public endpoint** — `GET /api/featured`
- Returns an ordered array (≤6) of featured games with the rail's fields:
  `id, slug, title, developer_name, genre, tags, avg_rating, rating_count,
  play_count, screenshots (first 4), header_image, cover_path, theme_color,
  description, is_webgpu`.
- Implemented as a new `models` query: pinned (`featured_rank>0 ORDER BY
  featured_rank`) UNION-style merged with the blended auto-fill, deduped,
  `published = 1`, limit 6.
- Trending sub-query uses the indexed `game_views(game_id, created_at)`:
  `COUNT(*) ... WHERE created_at >= datetime('now','-7 days') GROUP BY game_id`.
- Register in `internal/server/server.go` with `AuthOptional` (same as
  `/api/games`); read-only, no CSRF concern. Light rate-limit optional.

**Admin endpoints** — under the existing `/api/admin` group (`AdminRequired()`,
returns 404 on deny):
- `GET /api/admin/featured` — current pinned list (ordered) + a browsable list
  of candidate games to pin.
- `PUT /api/admin/featured` — body `{ "game_ids": ["id1","id2", ...] }` sets the
  pinned set/order: reset all `featured_rank` to 0, then assign 1..N in array
  order. Validates each id exists and is published.

### Frontend — Phase 1

In `#store-section`, insert a hero container **above** `.filter-bar`:

```html
<div class="store-hero" id="store-hero" style="display:none;"></div>
```

- New `loadStoreHero()` called from `navigate('store')` (alongside `loadStore()`
  / `loadRecentlyPlayed()` at ~line 1072). Fetches `/api/featured`; if empty,
  keep `#store-hero` hidden.
- Render hero + filmstrip via `innerHTML` template strings, all user data
  through `escapeHtml()`.
- Module-level hero state: `heroGames[]`, `heroIndex`, `heroTimer`.
  `selectHeroSlot(i)` swaps the capsule/rail and re-marks the active thumb.
- **CSP compliance (critical):** no inline `on*=` handlers. Use the existing
  delegation helpers — `act('selectHeroSlot', i)` for filmstrip clicks,
  `act('showGameDetail', id)` / play action for Play Now, `onEv(...)` for
  hover-pause if not done in CSS. Hover *visuals* go in CSS, not handlers.
- Auto-advance timer set in JS; cleared on hover/focus; not started when
  `matchMedia('(prefers-reduced-motion: reduce)').matches`.
- New CSS block (reuse the existing Steam-ish palette: `#2a475e`/`#171a21`,
  accent `var(--accent)`). Group near the `.recently-played` styles.

### Out of scope for Phase 1

- Themed rows and "Browse all" restructure (Phase 2).
- Personalization of any kind.
- Per-developer featured (`developer_pages.featured_games`) is unrelated and
  untouched.

## Phase 2 — Themed rows (end-state outline)

Built after Phase 1 ships. The page becomes:

1. Featured hero (Phase 1)
2. **New & Trending** — horizontal scroll row
3. **Top Rated** — horizontal scroll row (min review threshold)
4. **Recently Played** — horizontal scroll row, **logged-in only** (reuses the
   existing `loadRecentlyPlayed()` data source)
5. **Browse all** — the existing `.filter-bar` + `#store-grid` + load-more,
   relabeled as a section

Implementation notes (provisional):
- Rows 2–4 reuse `gameCardHTML()` inside a horizontal `.recently-scroll`-style
  container.
- New & Trending / Top Rated can reuse `/api/games?sort=...&limit=...`
  (`popular`, `rating`, `newest`) rather than new endpoints, or a single
  `/api/store/sections` aggregate to cut round-trips — decide at build time.
- Keep search/filter fully functional in "Browse all".

## Testing

No automated suite (per project convention). Manual verification:

1. `go build -o playmore && ./playmore`, then `POST /api/seed` for demo data.
2. Hero renders; auto-advances ~6s; pauses on hover/focus; respects reduced
   motion.
3. Filmstrip click switches the featured game; active thumb is marked.
4. A game **with** screenshots shows the 2×2 grid; a game **without** shows the
   description fallback — no empty hole.
5. As admin: pin games via the admin featured UI; confirm they lead the hero in
   order, and auto-fill (trending+newest blend) fills the rest without dupes.
6. Fewer than 6 published games → filmstrip shrinks; zero → hero hidden, grid
   shows.
7. CSP: no console violations; all hero interactions work with
   `script-src-attr 'none'` (no inline handlers).
8. Logged-out vs logged-in: hero identical (no personalization).

## Open questions / decisions deferred to build

- Exact rating-label thresholds (Very Positive / Positive / Mixed) — match the
  game detail page if it already defines them.
- Whether Phase 2 uses per-row `/api/games` calls or one aggregate endpoint.
- "Most-played" window: spec uses `game_views` last 7 days; confirm
  `game_views` is the intended popularity proxy vs. `playtime.play_count`.
```
