# Store Featured Hero (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Steam-style featured hero (split capsule + info rail + filmstrip carousel) to the top of the PlayMore store page, fed by admin pins + trending/newest auto-fill.

**Architecture:** A new `featured_rank` column on `games` records admin pins. A pure `MergeFeaturedIDs` function combines pinned IDs with trending (most `game_views` in 7 days) and newest IDs, deduped and limited. A public `GET /api/featured` serves the hero; admin `GET/PUT /api/admin/featured` manage pins. The frontend SPA renders the hero into a new `#store-hero` container with a screenshot/description-fallback rail, a filmstrip selector, and a hover/focus-pausing auto-advance timer. All wiring goes through the existing CSP-safe `act()`/`onEv()` delegation helpers (no inline handlers).

**Tech Stack:** Go 1.x + Gin, `modernc.org/sqlite` (pure Go), vanilla JS single-file SPA (`frontend/index.html`). Tests: Go's built-in `go test` for the pure merge logic; manual build + browser verification for DB/HTTP/UI (the project has no frontend test harness — see CLAUDE.md "Testing").

**Branch:** `feature/store-featured-hero` (already created).

**Scope:** Phase 1 only (the hero). Phase 2 themed rows are out of scope (see the design doc `docs/superpowers/specs/2026-06-15-store-featured-hero-design.md`).

---

## File Structure

- **Modify** `internal/storage/db.go` — append two idempotent migrations (column + index).
- **Create** `internal/models/featured.go` — pin storage, the three ID queries, `MergeFeaturedIDs`, and assemblers (`GetFeaturedGames`, `GetPinnedGames`, `SetFeaturedPins`).
- **Create** `internal/models/featured_test.go` — `go test` for `MergeFeaturedIDs`.
- **Create** `internal/handlers/featured.go` — `GetFeatured`, `AdminGetFeatured`, `AdminSetFeatured`.
- **Modify** `internal/server/server.go` — register one public + two admin routes.
- **Modify** `frontend/index.html` — hero CSS, `#store-hero` container, hero JS (load/render/select/auto-advance), `navigate()` wiring, and the admin "Featured" pin-management tab.

Why this split: the pure merge logic is isolated in `featured.go` so it can be unit-tested without a DB; the DB queries reuse the existing `scanGameRow`/`GetGamesByIDs` helpers in `game.go`; handlers stay thin and mirror `games.go`.

---

## Task 1: Schema migration — `featured_rank` column + index

**Files:**
- Modify: `internal/storage/db.go` (the `migrations` string slice inside `migrate()`)

- [ ] **Step 1: Append the two migrations**

In `internal/storage/db.go`, find the `migrations := []string{ ... }` slice in `migrate()` (it already contains entries like `` `ALTER TABLE games ADD COLUMN videos TEXT DEFAULT '[]'` ``). Append these two entries at the **end** of the slice (never edit existing entries — they are applied in order and must stay append-only):

```go
		`ALTER TABLE games ADD COLUMN featured_rank INTEGER DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_games_featured_rank ON games(featured_rank)`,
```

- [ ] **Step 2: Build to verify it compiles and migrates**

Run: `go build -o playmore && ./playmore --data /tmp/pm-hero-test &` then wait 2s and `curl -s localhost:8080/api/games | head -c 80; echo; kill %1`
Expected: server starts with no migration error in the log (a successful boot prints the listening line); the `curl` returns JSON (`{"games":...`). The `ALTER TABLE` runs once; re-running `./playmore` on the same `--data` dir must not error (idempotent: duplicate-column errors are swallowed by the existing migrate() runner — confirm the second boot is clean).

- [ ] **Step 3: Commit**

```bash
git add internal/storage/db.go
git commit -m "feat: add featured_rank column for store hero pins"
```

---

## Task 2: Pure merge function + unit test (TDD)

**Files:**
- Create: `internal/models/featured.go`
- Test: `internal/models/featured_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/models/featured_test.go`:

```go
package models

import (
	"reflect"
	"testing"
)

func TestMergeFeaturedIDs(t *testing.T) {
	tests := []struct {
		name     string
		pinned   []string
		trending []string
		newest   []string
		limit    int
		want     []string
	}{
		{
			name:     "pins first then alternate trending/newest",
			pinned:   []string{"p1", "p2"},
			trending: []string{"t1", "t2"},
			newest:   []string{"n1", "n2"},
			limit:    6,
			want:     []string{"p1", "p2", "t1", "n1", "t2", "n2"},
		},
		{
			name:     "dedup across sources",
			pinned:   []string{"a"},
			trending: []string{"a", "t1"},
			newest:   []string{"t1", "n1"},
			limit:    6,
			want:     []string{"a", "t1", "n1"},
		},
		{
			name:     "limit truncates",
			pinned:   []string{"p1"},
			trending: []string{"t1", "t2", "t3"},
			newest:   []string{"n1", "n2"},
			limit:    3,
			want:     []string{"p1", "t1", "n1"},
		},
		{
			name:     "trending exhausted falls back to newest",
			pinned:   nil,
			trending: []string{"t1"},
			newest:   []string{"n1", "n2", "n3"},
			limit:    4,
			want:     []string{"t1", "n1", "n2", "n3"},
		},
		{
			name:     "empty everything returns empty slice",
			pinned:   nil,
			trending: nil,
			newest:   nil,
			limit:    6,
			want:     []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeFeaturedIDs(tt.pinned, tt.trending, tt.newest, tt.limit)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (compile error — function undefined)**

Run: `go test ./internal/models/ -run TestMergeFeaturedIDs`
Expected: FAIL — `undefined: MergeFeaturedIDs`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/models/featured.go`:

```go
package models

import (
	"database/sql"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// MergeFeaturedIDs builds the ordered featured list: pinned IDs first (in the
// given order), then auto-fill slots alternating trending/newest, skipping any
// ID already used, until `limit` is reached or both sources are exhausted.
func MergeFeaturedIDs(pinned, trending, newest []string, limit int) []string {
	if limit < 0 {
		limit = 0
	}
	result := make([]string, 0, limit)
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || seen[id] || len(result) >= limit {
			return
		}
		seen[id] = true
		result = append(result, id)
	}
	for _, id := range pinned {
		add(id)
	}
	ti, ni := 0, 0
	useTrending := true
	for len(result) < limit && (ti < len(trending) || ni < len(newest)) {
		if useTrending && ti < len(trending) {
			add(trending[ti])
			ti++
		} else if !useTrending && ni < len(newest) {
			add(newest[ni])
			ni++
		} else if ti < len(trending) {
			add(trending[ti])
			ti++
		} else if ni < len(newest) {
			add(newest[ni])
			ni++
		}
		useTrending = !useTrending
	}
	return result
}

// --- DB-backed ID sources (implemented in Task 3) live in this file too. ---
var _ = sql.ErrNoRows // placeholder; removed when Task 3 adds real usage
var _ = storage.DB     // placeholder; removed when Task 3 adds real usage
```

> Note: the two `var _ =` placeholder lines exist only so the file compiles with its imports before Task 3 adds the DB queries. **Task 3 deletes both placeholder lines.**

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/models/ -run TestMergeFeaturedIDs -v`
Expected: PASS — all five subtests `--- PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/models/featured.go internal/models/featured_test.go
git commit -m "feat: MergeFeaturedIDs pure merge logic with tests"
```

---

## Task 3: DB query functions + assemblers

**Files:**
- Modify: `internal/models/featured.go`

- [ ] **Step 1: Replace the placeholder lines with the DB functions**

In `internal/models/featured.go`, delete the two `var _ = ...` placeholder lines from Task 2 and append these functions (the package already imports `database/sql` and `storage`):

```go
// scanIDColumn collects a single-column id result set.
func scanIDColumn(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// featuredPinnedIDs returns published, admin-pinned game IDs in pin order.
func featuredPinnedIDs() ([]string, error) {
	return scanIDColumn(storage.DB.Query(
		`SELECT id FROM games WHERE featured_rank > 0 AND published = 1 ORDER BY featured_rank ASC`,
	))
}

// trendingIDs7d returns published game IDs ranked by view count in the last 7 days.
func trendingIDs7d(limit int) ([]string, error) {
	return scanIDColumn(storage.DB.Query(
		`SELECT g.id FROM games g
		 JOIN game_views v ON v.game_id = g.id
		 WHERE g.published = 1 AND v.created_at >= datetime('now','-7 days')
		 GROUP BY g.id
		 ORDER BY COUNT(*) DESC
		 LIMIT ?`, limit,
	))
}

// newestIDs returns the most recently published game IDs.
func newestIDs(limit int) ([]string, error) {
	return scanIDColumn(storage.DB.Query(
		`SELECT id FROM games WHERE published = 1 ORDER BY created_at DESC LIMIT ?`, limit,
	))
}

// GetFeaturedGames assembles the hero list: pins first, then a trending+newest
// blend, deduped and limited. Returns full Game structs in display order.
func GetFeaturedGames(limit int) ([]Game, error) {
	if limit < 1 || limit > 12 {
		limit = 6
	}
	pinned, err := featuredPinnedIDs()
	if err != nil {
		return nil, err
	}
	trending, err := trendingIDs7d(limit)
	if err != nil {
		return nil, err
	}
	newest, err := newestIDs(limit)
	if err != nil {
		return nil, err
	}
	ids := MergeFeaturedIDs(pinned, trending, newest, limit)
	if len(ids) == 0 {
		return []Game{}, nil
	}
	return GetGamesByIDs(ids) // already reorders to match input id order
}

// GetPinnedGames returns the currently pinned games (for the admin UI), ordered.
func GetPinnedGames() ([]Game, error) {
	ids, err := featuredPinnedIDs()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []Game{}, nil
	}
	return GetGamesByIDs(ids)
}

// SetFeaturedPins replaces the entire pin set: clears all ranks, then assigns
// 1..N in the given id order. Runs in a transaction (DB is SetMaxOpenConns(1)).
func SetFeaturedPins(ids []string) error {
	tx, err := storage.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE games SET featured_rank = 0 WHERE featured_rank > 0`); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE games SET featured_rank = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 2: Verify the package compiles and existing tests still pass**

Run: `go build ./... && go test ./internal/models/ -run TestMergeFeaturedIDs`
Expected: build succeeds (no unused-import errors now that the placeholders are gone), test PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/models/featured.go
git commit -m "feat: featured DB queries and assemblers (pins/trending/newest)"
```

---

## Task 4: Public endpoint `GET /api/featured`

**Files:**
- Create: `internal/handlers/featured.go`
- Modify: `internal/server/server.go` (public routes block, near `api.GET("/games", handlers.ListGames)` ~line 154)

- [ ] **Step 1: Create the handler**

Create `internal/handlers/featured.go`:

```go
package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

// GetFeatured returns the curated store-hero list (pins + trending/newest fill).
func GetFeatured(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "6"))
	games, err := models.GetFeaturedGames(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load featured"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"games": games})
}
```

- [ ] **Step 2: Register the route**

In `internal/server/server.go`, immediately after the line `api.GET("/games/:id", handlers.GetGame)` (~line 155), add:

```go
		api.GET("/featured", handlers.GetFeatured)
```

- [ ] **Step 3: Build, run, seed, and verify the endpoint**

Run:
```bash
go build -o playmore && ./playmore --data /tmp/pm-hero-test &
sleep 2
curl -s -X POST localhost:8080/api/seed >/dev/null
curl -s 'localhost:8080/api/featured?limit=6' | head -c 400; echo
kill %1
```
Expected: JSON `{"games":[ ... ]}` with up to 6 game objects, each including `title`, `developer_name`, `screenshots`, `header_image`, `avg_rating`, `review_count`. With freshly seeded data and no views yet, the list is filled by the "newest" source — that is correct behavior.

- [ ] **Step 4: Commit**

```bash
git add internal/handlers/featured.go internal/server/server.go
git commit -m "feat: GET /api/featured store hero endpoint"
```

---

## Task 5: Admin endpoints `GET`/`PUT /api/admin/featured`

**Files:**
- Modify: `internal/handlers/featured.go`
- Modify: `internal/server/server.go` (admin group, ~lines 239-248)

- [ ] **Step 1: Add the admin handlers**

Append to `internal/handlers/featured.go`:

```go
// AdminGetFeatured returns the current pinned games (admin only).
func AdminGetFeatured(c *gin.Context) {
	games, err := models.GetPinnedGames()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load pins"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"games": games})
}

// AdminSetFeatured replaces the pinned set/order (admin only).
func AdminSetFeatured(c *gin.Context) {
	var input struct {
		GameIDs []string `json:"game_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"})
		return
	}
	if len(input.GameIDs) > 12 {
		input.GameIDs = input.GameIDs[:12]
	}
	if err := models.SetFeaturedPins(input.GameIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"game_ids": input.GameIDs})
}
```

- [ ] **Step 2: Register the admin routes**

In `internal/server/server.go`, inside the `admin := api.Group("/admin")` block (after the existing `admin.GET("/games", ...)` / `admin.PUT("/games/:id/publish", ...)` lines, ~line 247), add:

```go
		admin.GET("/featured", middleware.RateLimit(120, 3600), handlers.AdminGetFeatured)
		admin.PUT("/featured", middleware.RateLimit(60, 3600), handlers.AdminSetFeatured)
```

- [ ] **Step 3: Build and verify admin auth gating (404 when unauthenticated)**

Run:
```bash
go build -o playmore && ./playmore --data /tmp/pm-hero-test &
sleep 2
echo "--- unauth GET (expect 404) ---"
curl -s -o /dev/null -w "%{http_code}\n" localhost:8080/api/admin/featured
kill %1
```
Expected: `404` (admin endpoints return 404, not 403, to hide existence — per CLAUDE.md). Authenticated admin verification happens via the browser UI in Task 8.

- [ ] **Step 4: Commit**

```bash
git add internal/handlers/featured.go internal/server/server.go
git commit -m "feat: admin GET/PUT /api/admin/featured pin management"
```

---

## Task 6: Frontend — hero container, CSS, render + fallback + filmstrip

**Files:**
- Modify: `frontend/index.html` (CSS block, `#store-section` markup, hero JS, `navigate()` wiring)

- [ ] **Step 1: Add the hero CSS**

In `frontend/index.html`, find the `/* Recently Played */` CSS comment (~line 375) and insert this block immediately **before** it:

```css
        /* Featured Hero */
        .store-hero { margin-bottom: 30px; }
        .hero-main { display: flex; gap: 14px; }
        .hero-capsule { position: relative; flex: 2; min-height: 320px; border-radius: 6px; background-size: cover; background-position: center; background-color: #1b2838; cursor: pointer; overflow: hidden; border: 1px solid rgba(102,192,244,0.3); }
        .hero-capsule::after { content: ''; position: absolute; inset: 0; background: linear-gradient(0deg, rgba(8,14,18,0.85) 0%, rgba(8,14,18,0) 55%); }
        .hero-live { position: absolute; top: 12px; left: 12px; z-index: 2; background: #cd4646; color: #fff; font-size: 11px; font-weight: 600; padding: 3px 9px; border-radius: 3px; }
        .hero-capsule-title { position: absolute; bottom: 18px; left: 20px; z-index: 2; color: #fff; font-size: 38px; font-weight: 800; text-shadow: 0 2px 12px #000; line-height: 1; }
        .hero-rail { flex: 1; display: flex; flex-direction: column; gap: 9px; min-width: 0; }
        .hero-rail-title { color: #fff; font-size: 16px; font-weight: 700; }
        .hero-rail-rating { color: var(--success); font-size: 12px; }
        .hero-shots { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; }
        .hero-shot { padding-top: 56%; background-size: cover; background-position: center; border-radius: 3px; background-color: #22303c; }
        .hero-desc { font-size: 13px; line-height: 1.5; color: var(--text-secondary); display: -webkit-box; -webkit-line-clamp: 6; -webkit-box-orient: vertical; overflow: hidden; }
        .hero-rail-dev { color: var(--text-secondary); font-size: 12px; }
        .hero-tags { display: flex; gap: 5px; flex-wrap: wrap; }
        .hero-tag { font-size: 10px; background: rgba(255,255,255,0.08); color: #9fb4c4; padding: 3px 8px; border-radius: 3px; cursor: pointer; }
        .hero-tag-webgpu { background: rgba(102,192,244,0.15); color: var(--accent); }
        .hero-play { margin-top: auto; height: 38px; border: none; border-radius: 4px; background: linear-gradient(90deg, #1a6dc4, var(--accent)); color: #fff; font-size: 13px; font-weight: 700; cursor: pointer; }
        .hero-play:hover { filter: brightness(1.1); }
        .hero-strip { display: flex; gap: 8px; margin-top: 12px; }
        .hero-thumb { flex: 1; height: 54px; border-radius: 4px; background-size: cover; background-position: center; background-color: #243240; cursor: pointer; opacity: 0.6; border: 2px solid transparent; transition: opacity 0.2s; }
        .hero-thumb:hover { opacity: 0.85; }
        .hero-thumb.active { opacity: 1; border-color: var(--accent); }
        @media (max-width: 700px) { .hero-main { flex-direction: column; } .hero-capsule { min-height: 200px; } }
```

- [ ] **Step 2: Add the hero container to the store section**

In `frontend/index.html`, find `<section id="store-section" class="section active">` (~line 506). Insert the hero container as the section's first child, immediately **before** the `<div class="recently-played" id="recently-played" ...>` line:

```html
            <div class="store-hero" id="store-hero" style="display:none;"></div>
```

- [ ] **Step 3: Add the hero JS (load + render + select)**

In `frontend/index.html`, find the end of `gameCardHTML()` (the `/* ============ Library ============ */` comment is right after it, ~line 1312). Insert this block immediately **before** that `/* ============ Library ============ */` comment:

```javascript
        /* ============ Featured Hero ============ */
        let heroGames = [];
        let heroIndex = 0;
        let heroTimer = null;
        let heroListenersAttached = false;
        const HERO_INTERVAL = 6000;

        async function loadStoreHero() {
            const el = document.getElementById('store-hero');
            if (!el) return;
            try {
                const data = await api('/featured?limit=6');
                heroGames = data.games || [];
            } catch (e) {
                heroGames = [];
            }
            if (heroGames.length === 0) { el.style.display = 'none'; return; }
            el.style.display = 'block';
            heroIndex = 0;
            renderHero();
            attachHeroListeners();
            startHeroAuto();
        }

        function heroRatingLabel(avg, count) {
            if (!count || count === 0) return 'No ratings yet';
            let label = 'Mixed';
            if (avg >= 4.5) label = 'Overwhelmingly Positive';
            else if (avg >= 4.0) label = 'Very Positive';
            else if (avg >= 3.0) label = 'Positive';
            else if (avg >= 2.0) label = 'Mixed';
            else label = 'Negative';
            return '★ ' + label + ' · ' + count + (count === 1 ? ' rating' : ' ratings');
        }

        function heroImage(g) {
            if (g.header_image) return g.header_image;       // sanitized http(s) URL (server-side)
            const cover = playAsset(g.cover_path);
            if (cover) return cover;
            return 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="600" height="340"><rect fill="#1b2838" width="600" height="340"/></svg>');
        }

        function renderHero() {
            const el = document.getElementById('store-hero');
            if (!el || heroGames.length === 0) return;
            const g = heroGames[heroIndex];
            const shots = (g.screenshots || []).slice(0, 4);
            let railMedia;
            if (shots.length > 0) {
                railMedia = '<div class="hero-shots">' + shots.map(s =>
                    '<div class="hero-shot" style="background-image:url(\'' + escapeHtml(playAsset(s)) + '\')"></div>'
                ).join('') + '</div>';
            } else {
                railMedia = '<div class="hero-desc">' + escapeHtml(g.description || '') + '</div>';
            }
            const tags = [];
            if (g.genre) tags.push('<span class="hero-tag" ' + act('searchByTag', g.genre) + '>' + escapeHtml(g.genre) + '</span>');
            (g.tags || []).slice(0, 2).forEach(t => tags.push('<span class="hero-tag" ' + act('searchByTag', t) + '>' + escapeHtml(t) + '</span>'));
            if (g.is_webgpu) tags.push('<span class="hero-tag hero-tag-webgpu">WebGPU</span>');

            el.innerHTML =
                '<div class="hero-main">' +
                  '<div class="hero-capsule" ' + act('showGameDetail', g.id) + ' style="background-image:url(\'' + escapeHtml(heroImage(g)) + '\')">' +
                    '<span class="hero-live">● LIVE</span>' +
                    '<div class="hero-capsule-title">' + escapeHtml(g.title) + '</div>' +
                  '</div>' +
                  '<div class="hero-rail">' +
                    '<div class="hero-rail-title">' + escapeHtml(g.title) + '</div>' +
                    '<div class="hero-rail-rating">' + heroRatingLabel(g.avg_rating, g.review_count) + '</div>' +
                    railMedia +
                    '<div class="hero-rail-dev">by ' + escapeHtml(g.developer_name || 'Unknown') + '</div>' +
                    '<div class="hero-tags">' + tags.join('') + '</div>' +
                    '<button class="hero-play" ' + act('showGameDetail', g.id) + '>▶ PLAY NOW</button>' +
                  '</div>' +
                '</div>' +
                '<div class="hero-strip">' +
                  heroGames.map((hg, i) =>
                    '<div class="hero-thumb' + (i === heroIndex ? ' active' : '') + '" ' + act('selectHeroSlot', i) +
                    ' style="background-image:url(\'' + escapeHtml(heroImage(hg)) + '\')" title="' + escapeHtml(hg.title) + '" tabindex="0"></div>'
                  ).join('') +
                '</div>';
        }

        function selectHeroSlot(i) {
            if (i < 0 || i >= heroGames.length) return;
            heroIndex = i;
            renderHero();
            startHeroAuto(); // reset timer on manual interaction
        }
```

- [ ] **Step 4: Wire the hero into store navigation**

In `frontend/index.html`, find this line in `navigate()` (~line 1072):

```javascript
            if (tab === 'store') { loadStore(); loadRecentlyPlayed(); }
```

Replace it with:

```javascript
            if (tab === 'store') { loadStore(); loadRecentlyPlayed(); loadStoreHero(); }
```

> `startHeroAuto`, `stopHeroAuto`, and `attachHeroListeners` are defined in Task 7. Until Task 7 lands, `loadStoreHero()` will throw a ReferenceError when it reaches `attachHeroListeners()`/`startHeroAuto()` — so **do Step 5's manual check only after Task 7**. (If implementing strictly one task at a time with a working build between each, temporarily stub `function startHeroAuto(){} function stopHeroAuto(){} function attachHeroListeners(){}` and remove the stubs in Task 7. Otherwise implement Task 6 and Task 7 back-to-back before manual verification.)

- [ ] **Step 5: Build and manually verify the hero renders (after Task 7, or with stubs)**

Run:
```bash
go build -o playmore && ./playmore --data /tmp/pm-hero-test &
sleep 2
curl -s -X POST localhost:8080/api/seed >/dev/null
kill %1
echo "Now: ./playmore --data /tmp/pm-hero-test  and open http://localhost:8080/#store in a browser"
```
Expected in browser at `#store`: a hero appears above the filter bar — large capsule on the left with a "● LIVE" badge and the game title, an info rail on the right (title, rating line, 2×2 screenshots OR a description block when a game has no screenshots), developer, tag chips, and a "▶ PLAY NOW" button; a filmstrip of thumbnails sits below. Clicking a thumbnail switches the featured game. Clicking the capsule or PLAY NOW opens the game detail. No CSP violations in the console (no inline handlers were used).

- [ ] **Step 6: Commit**

```bash
git add frontend/index.html
git commit -m "feat: store featured hero render (capsule + rail + filmstrip)"
```

---

## Task 7: Frontend — auto-advance with hover/focus pause + reduced motion

**Files:**
- Modify: `frontend/index.html` (hero JS — add timer + listener functions)

- [ ] **Step 1: Add the auto-advance and listener functions**

In `frontend/index.html`, immediately **after** the `selectHeroSlot` function added in Task 6 (still before the `/* ============ Library ============ */` comment), add:

```javascript
        function startHeroAuto() {
            stopHeroAuto();
            if (heroGames.length <= 1) return;
            if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
            heroTimer = setInterval(function () {
                heroIndex = (heroIndex + 1) % heroGames.length;
                renderHero();
            }, HERO_INTERVAL);
        }

        function stopHeroAuto() {
            if (heroTimer) { clearInterval(heroTimer); heroTimer = null; }
        }

        // Pause on hover/focus. Listeners are attached once to the static
        // #store-hero container (innerHTML changes don't detach them). focusin/
        // focusout bubble from inner thumbs/buttons, so the whole hero pauses.
        function attachHeroListeners() {
            if (heroListenersAttached) return;
            const el = document.getElementById('store-hero');
            if (!el) return;
            el.addEventListener('mouseenter', stopHeroAuto);
            el.addEventListener('mouseleave', startHeroAuto);
            el.addEventListener('focusin', stopHeroAuto);
            el.addEventListener('focusout', startHeroAuto);
            heroListenersAttached = true;
        }
```

> If you added the temporary stubs in Task 6 Step 4, delete them now so these real definitions are the only ones.

- [ ] **Step 2: Build and manually verify auto-advance behavior**

Run: `go build -o playmore && ./playmore --data /tmp/pm-hero-test` then open `http://localhost:8080/#store`.
Expected (needs ≥2 featured games from the seed):
- The hero advances to the next slot roughly every 6 seconds; the active filmstrip thumb tracks it.
- Hovering the mouse over the hero pauses advancing; moving away resumes.
- Tab-focusing a filmstrip thumb (keyboard) pauses; blurring resumes.
- With OS "reduce motion" enabled, the hero does not auto-advance (manual filmstrip clicks still work).

- [ ] **Step 3: Commit**

```bash
git add frontend/index.html
git commit -m "feat: hero auto-advance with hover/focus pause and reduced-motion"
```

---

## Task 8: Frontend — admin "Featured" pin-management tab

**Files:**
- Modify: `frontend/index.html` (admin tab list + render branch + pin handlers)

- [ ] **Step 1: Add "featured" to the admin tab list**

In `frontend/index.html`, find this line in `loadAdmin()` (~line 3669):

```javascript
            ['overview', 'analytics', 'users', 'games'].forEach(t => {
```

Replace with:

```javascript
            ['overview', 'analytics', 'users', 'games', 'featured'].forEach(t => {
```

- [ ] **Step 2: Add the render branch**

In `loadAdmin()`, find the end of the `else if (adminTab === 'games') { ... }` block — the closing `}` immediately before `container.innerHTML = html;` (~line 3701). Insert this branch between them:

```javascript
                } else if (adminTab === 'featured') {
                    html += await renderAdminFeatured();
```

So it reads:

```javascript
                    });
                } else if (adminTab === 'featured') {
                    html += await renderAdminFeatured();
                }
                container.innerHTML = html;
```

- [ ] **Step 3: Reset the featured cache when switching admin tabs**

In `frontend/index.html`, find (~line 1883):

```javascript
        function setAdminTab(t) { adminTab = t; loadAdmin(); }
```

Replace with:

```javascript
        function setAdminTab(t) { adminTab = t; adminFeaturedLoaded = false; loadAdmin(); }
```

- [ ] **Step 4: Add the featured admin render + pin handlers**

In `frontend/index.html`, immediately **after** the `loadAdmin()` function's closing `}` (the `function barChart(...)` definition follows it, ~line 3708), insert:

```javascript
        /* ----- Admin: Featured pin management ----- */
        let adminFeaturedLoaded = false;
        let adminPinned = [];          // ordered array of pinned game ids (unsaved working copy)
        let adminGameMap = {};         // id -> game object (for titles/meta)
        let adminAllGames = [];        // candidate games to pin

        async function renderAdminFeatured() {
            if (!adminFeaturedLoaded) {
                const [pinnedData, allData] = await Promise.all([
                    api('/admin/featured').catch(() => ({ games: [] })),
                    api('/games?limit=50&sort=newest').catch(() => ({ games: [] })),
                ]);
                adminPinned = (pinnedData.games || []).map(g => g.id);
                adminAllGames = allData.games || [];
                adminGameMap = {};
                (pinnedData.games || []).concat(adminAllGames).forEach(g => { adminGameMap[g.id] = g; });
                adminFeaturedLoaded = true;
            }

            let h = '<p style="color:var(--text-secondary);font-size:13px;margin-bottom:12px;">Pinned games lead the store hero, in order. Empty slots auto-fill from trending + newest.</p>';
            h += '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;">';
            h += '<h3 style="font-size:14px;">Pinned (' + adminPinned.length + ')</h3>';
            h += '<button class="btn-accent" ' + act('adminSaveFeatured') + ' style="font-size:12px;padding:6px 14px;">Save</button></div>';

            if (adminPinned.length === 0) {
                h += '<div style="color:var(--text-secondary);font-size:12px;font-style:italic;margin-bottom:14px;">No pins — the hero is fully auto-filled.</div>';
            } else {
                adminPinned.forEach((id, i) => {
                    const g = adminGameMap[id] || { title: id };
                    h += '<div class="dashboard-card"><div></div><div><div class="game-card-title">' + (i + 1) + '. ' + escapeHtml(g.title) + '</div></div><div class="dashboard-actions">';
                    if (i > 0) h += '<button class="dash-btn dash-btn-edit" ' + act('adminMovePin', id, -1) + '>↑</button>';
                    if (i < adminPinned.length - 1) h += '<button class="dash-btn dash-btn-edit" ' + act('adminMovePin', id, 1) + '>↓</button>';
                    h += '<button class="dash-btn dash-btn-delete" ' + act('adminUnpin', id) + '>Unpin</button></div></div>';
                });
            }

            h += '<h3 style="font-size:14px;margin:16px 0 8px;">Add a game</h3>';
            adminAllGames.filter(g => adminPinned.indexOf(g.id) === -1).forEach(g => {
                h += '<div class="dashboard-card"><div></div><div><div class="game-card-title">' + escapeHtml(g.title) + '</div><div class="dashboard-stats"><span>by ' + escapeHtml(g.developer_name || 'Unknown') + '</span><span>' + escapeHtml(g.genre) + '</span></div></div><div class="dashboard-actions"><button class="dash-btn dash-btn-edit" ' + act('adminPin', g.id) + '>Pin</button></div></div>';
            });
            return h;
        }

        // These mutate the local working copy then re-render (loadAdmin keeps the
        // loaded cache because they don't reset adminFeaturedLoaded).
        function adminPin(id) { if (adminPinned.indexOf(id) === -1) adminPinned.push(id); loadAdmin(); }
        function adminUnpin(id) { adminPinned = adminPinned.filter(x => x !== id); loadAdmin(); }
        function adminMovePin(id, dir) {
            const i = adminPinned.indexOf(id), j = i + dir;
            if (i < 0 || j < 0 || j >= adminPinned.length) return;
            const t = adminPinned[i]; adminPinned[i] = adminPinned[j]; adminPinned[j] = t;
            loadAdmin();
        }
        async function adminSaveFeatured() {
            try {
                await api('/admin/featured', { method: 'PUT', body: JSON.stringify({ game_ids: adminPinned }) });
                toast('Featured games saved.', 'success');
            } catch (e) { toast('Failed to save featured games.', 'error'); }
        }
```

- [ ] **Step 5: Build and manually verify the full admin → hero loop**

Run: `go build -o playmore && ./playmore --data /tmp/pm-hero-test` then in a browser:
1. Register/log in as the **first** user (admin). Seed data first if needed: `curl -s -X POST localhost:8080/api/seed`.
2. Go to `#admin` → click the **Featured** tab.
3. Pin 2 games via "Pin", reorder with ↑/↓, click **Save** (toast confirms).
4. Go to `#store` and confirm the pinned games lead the hero filmstrip in the chosen order, with the remaining slots auto-filled.
5. Unpin all, Save, reload `#store` — hero is fully auto-filled again (no errors).

Expected: all steps work; no console CSP violations; admin tab is invisible/404 to non-admins (already enforced server-side).

- [ ] **Step 6: Commit**

```bash
git add frontend/index.html
git commit -m "feat: admin Featured tab to pin/reorder store hero games"
```

---

## Final Verification (whole feature)

- [ ] **Run the Go test suite**

Run: `go test ./...`
Expected: PASS (at minimum `internal/models` `TestMergeFeaturedIDs`). Per the user's standing rule, all tests must pass — do not skip.

- [ ] **Full manual smoke test**

With seeded demo data, confirm end to end: `/api/featured` returns games; the `#store` hero renders, auto-advances, pauses on hover/focus, respects reduced motion; the rail shows screenshots or the description fallback; the filmstrip switches games; the admin Featured tab pins/reorders/saves and is reflected in the hero; zero featured games hides the hero and the grid still shows.

- [ ] **Confirm CSP cleanliness**

In the browser devtools console on `#store` and `#admin`, confirm there are **no** Content-Security-Policy violation errors (every interaction goes through `act()`/`onEv()` delegation; no inline `on*=` handlers were introduced).

---

## Self-Review (completed during planning)

- **Spec coverage:** split layout + filmstrip (Task 6), screenshot/description fallback (Task 6), no price/personalization (Task 6 — neither is rendered), admin pins + blend auto-fill (Tasks 2–5), trending=`game_views` 7d + newest (Task 3), 6 slots + ~6s auto-advance + hover/focus pause + reduced motion (Tasks 6–7), `featured_rank` migration + `/api/featured` + admin endpoints (Tasks 1, 4, 5), CSP-safe wiring (Tasks 6–8), empty/no-screenshot edge cases (Tasks 3, 6). Phase 2 rows intentionally excluded.
- **Type/name consistency:** `MergeFeaturedIDs`, `GetFeaturedGames`, `GetPinnedGames`, `SetFeaturedPins`, `GetFeatured`, `AdminGetFeatured`, `AdminSetFeatured` used identically across model→handler→route tasks. Frontend `loadStoreHero`/`renderHero`/`selectHeroSlot`/`startHeroAuto`/`stopHeroAuto`/`attachHeroListeners` and the `heroGames`/`heroIndex`/`heroTimer`/`heroListenersAttached` state are consistent across Tasks 6–7. Admin `adminPinned`/`adminGameMap`/`adminAllGames`/`adminFeaturedLoaded` consistent across Task 8.
- **Cross-task dependency flagged:** Task 6's `loadStoreHero` calls functions defined in Task 7 — noted explicitly with a stub workaround so each task can still build green.
```
