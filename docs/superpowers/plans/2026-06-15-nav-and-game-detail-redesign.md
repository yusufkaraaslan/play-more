# Nav & Game Detail Page Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure PlayMore's global nav into grouped left/right clusters with an account dropdown, and reorganize the game detail page into a Steam-style two-column layout with a sticky sidebar Play box.

**Architecture:** Pure frontend edits to the single embedded file `frontend/index.html`. Reuse the existing `2fr/1fr` `.game-layout` grid, the existing `.game-sidebar`, and the existing `act()`/`onEv()` delegated-event system. The account dropdown copies the existing `toggleFeed`/`feed-dropdown` + outside-click pattern. No Go/API/DB changes.

**Tech Stack:** Vanilla JS SPA in `frontend/index.html` (inline CSS/JS, `innerHTML` template strings), served via `go:embed` from a Go/Gin binary. CSP forbids inline `on*=` handlers — all events go through `data-act`/`data-<event>` attributes produced by `act()`/`onEv()`.

**Spec:** `docs/superpowers/specs/2026-06-15-nav-and-game-detail-redesign-design.md`

---

## Testing reality (read first)

This project has **no automated test suite** (per `CLAUDE.md`: "Test manually: build, seed demo data, exercise flows in browser"). The frontend is embedded via `go:embed`, so **every change requires a rebuild** to be visible. The verification gate for each task is:

1. `go build -o playmore` succeeds (this is the only automated check — it compiles the embedded asset).
2. Manual browser observation of the specific behavior the task changed.

**One-time setup for manual checks** (do once, reuse across tasks):

```bash
go build -o playmore && ./playmore        # serves http://localhost:8080
# In a second shell, seed demo games:
curl -X POST localhost:8080/api/seed
# In the browser at http://localhost:8080: register a user (the FIRST registered
# user becomes admin). Use this account for all "logged in" checks.
```

After each code change: stop the server (Ctrl-C), re-run `go build -o playmore && ./playmore`, hard-refresh the browser (Cmd/Ctrl-Shift-R).

Do not skip the manual checks — they are the test suite here.

---

## File Structure

All changes are in **`frontend/index.html`** (single file, ~4487 lines). Regions touched:

- **CSS block** (`<style>` in `<head>`): `.header` rule at line 60; new nav classes; new game-detail classes; mobile override near line 464.
- **Static header markup** (`<header class="header">`, lines 518–538): regroup, remove Profile tab, drop vestigial gpu-badge.
- **`renderHeader()`** (lines 932–953): replace `Welcome, <user>` + Logout with the account chip + dropdown.
- **Helper/handler functions** (script body): add `initials()`, `toggleAccountMenu()`, `closeAccountMenu()`, `openSettings()`, and an outside-click + Esc listener (placed next to the existing feed close listener near line 4068).
- **`showGameDetail()`** (lines 2052–2306): replace the top action-bar + header block (2064–2106) with breadcrumb+title; insert capsule + Play box + review summary at the top of the sidebar (after line 2243).

---

## Task 1: Regroup the static nav header (markup + CSS)

Goal: brand + tabs hug the left, a spacer pushes search → theme → actions to the right. Remove `Profile` from the tab row (it moves to the account dropdown in Task 2). The page must still work after this task (logged-in still shows old Welcome/Logout until Task 2).

**Files:**
- Modify: `frontend/index.html` — `.header` CSS (line 60) + new nav CSS; static `<header>` markup (lines 518–538).

- [ ] **Step 1: Change the `.header` rule and add nav-group CSS**

Replace line 60:

```css
        .header { background: var(--bg-secondary); padding: 0 20px; height: 60px; display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid var(--border-color); position: sticky; top: 0; z-index: 1000; }
```

with (drops `space-between`, adds a gap; the spacer element created in Step 2 does the grouping):

```css
        .header { background: var(--bg-secondary); padding: 0 20px; height: 60px; display: flex; align-items: center; gap: 14px; border-bottom: 1px solid var(--border-color); position: sticky; top: 0; z-index: 1000; }
        .header-spacer { flex: 1; }
        .account { position: relative; }
        .account-chip { display: flex; align-items: center; gap: 8px; background: rgba(255,255,255,0.05); border: 1px solid var(--border-color); border-radius: 99px; padding: 4px 12px 4px 4px; cursor: pointer; color: var(--text-primary); font-size: 13px; }
        .account-chip:hover { border-color: var(--accent); }
        .account-avatar { width: 28px; height: 28px; border-radius: 50%; background: linear-gradient(135deg, var(--accent), #2a6f97); color: #0b1620; display: flex; align-items: center; justify-content: center; font-size: 11px; font-weight: 700; flex-shrink: 0; }
        .account-menu { display: none; position: absolute; top: 46px; right: 0; min-width: 170px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 8px; overflow: hidden; z-index: 1001; box-shadow: 0 10px 40px rgba(0,0,0,0.5); }
        .account-menu.open { display: block; }
        .account-menu button { display: block; width: 100%; text-align: left; padding: 10px 14px; background: none; border: none; color: var(--text-primary); font-size: 13px; cursor: pointer; }
        .account-menu button:hover { background: rgba(255,255,255,0.06); color: var(--accent); }
```

- [ ] **Step 2: Replace the static `<header>` markup**

Replace the whole block, lines 518–538 (the existing `<header class="header"> … </header>`), with:

```html
    <header class="header">
        <div class="logo" data-act="navigate" data-act-args="[&quot;store&quot;]">
            <svg viewBox="0 0 24 24"><path d="M21 6H3c-1.1 0-2 .9-2 2v8c0 1.1.9 2 2 2h18c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2zm-10 7H8v3H6v-3H3v-2h3V8h2v3h3v2zm4.5 2c-.83 0-1.5-.67-1.5-1.5s.67-1.5 1.5-1.5 1.5.67 1.5 1.5-.67 1.5-1.5 1.5zm4-3c-.83 0-1.5-.67-1.5-1.5S18.67 9 19.5 9s1.5.67 1.5 1.5-.67 1.5-1.5 1.5z"/></svg>
            PlayMore
        </div>
        <nav class="nav-tabs" role="navigation" aria-label="Main navigation">
            <button class="nav-tab active" data-tab="store" data-act="navigate" data-act-args="[&quot;store&quot;]" tabindex="0">Store</button>
            <button class="nav-tab" data-tab="library" data-act="navigate" data-act-args="[&quot;library&quot;]" tabindex="0">Library</button>
            <button class="nav-tab" data-tab="feed" data-act="navigate" data-act-args="[&quot;feed&quot;]" id="nav-feed" style="display:none;" tabindex="0">Feed</button>
            <button class="nav-tab" data-tab="dev" data-act="navigate" data-act-args="[&quot;dev&quot;]" tabindex="0">Dev</button>
        </nav>
        <div class="header-spacer"></div>
        <div class="global-search">
            <input type="text" id="global-search-input" placeholder="Search games..." aria-label="Search games" data-input="debounceGlobalSearch" data-focus="debounceGlobalSearch" autocomplete="off">
            <div class="global-search-results" id="global-search-results"></div>
        </div>
        <button data-act="toggleTheme" id="theme-toggle" title="Toggle theme" style="background:none;border:none;font-size:18px;cursor:pointer;padding:5px;">🌙</button>
        <div class="header-actions" id="header-actions"></div>
    </header>
```

Changes from the original: `Profile` tab removed; `header-spacer` inserted after `</nav>`; search + theme moved ahead of `header-actions`; the vestigial `<span id="gpu-badge">` removed (it lived inside `#header-actions`, whose contents are overwritten by `renderHeader()` on every auth check — nothing reads `#gpu-badge` by id, confirmed by grep, so removing it changes no behavior).

- [ ] **Step 3: Build**

Run: `go build -o playmore`
Expected: exits 0, no output.

- [ ] **Step 4: Manual check (logged out)**

Rebuild+run, open `http://localhost:8080` logged out. Expected: logo + `Store Library Dev` hug the left (no `Profile`, `Feed` hidden when logged out); search box + 🌙 + `Login`/`Register` are pushed to the right edge as a group; nothing floats in dead center. Theme toggle still flips light/dark on click.

- [ ] **Step 5: Commit**

```bash
git add frontend/index.html
git commit -m "feat(nav): group header into left/right clusters, drop Profile tab"
```

---

## Task 2: Account chip + dropdown menu

Goal: replace the loose `Welcome, <username>` + `Logout` with an avatar-initials chip that opens a `Profile / Settings / Logout` dropdown. Theme + bell stay as-is.

**Files:**
- Modify: `frontend/index.html` — `renderHeader()` logged-in branch (lines 938–949); add helpers/handlers in the script body; add outside-click + Esc listener near line 4073.

- [ ] **Step 1: Replace the logged-in `renderHeader()` innerHTML**

In `renderHeader()`, replace the assignment in the `if (currentUser) {` branch (lines 939–949) with:

```javascript
                el.innerHTML = '<div style="position:relative;">' +
                    '<button class="feed-btn" ' + act('toggleFeed') + ' title="Notifications">🔔</button>' +
                    '<div class="feed-dropdown" id="feed-dropdown">' +
                    '<div class="feed-tabs">' +
                    '<button class="feed-tab active" ' + act('switchFeedTab', 'activity') + '>Activity</button>' +
                    '<button class="feed-tab" ' + act('switchFeedTab', 'library') + '>Library</button>' +
                    '<button class="feed-tab" ' + act('switchFeedTab', 'wishlist') + '>Wishlist</button>' +
                    '<button class="feed-tab" ' + act('switchFeedTab', 'following') + '>Following</button>' +
                    '</div><div class="feed-list" id="feed-list"></div></div></div> ' +
                    '<div class="account">' +
                    '<button class="account-chip" id="account-chip" ' + act('toggleAccountMenu') + ' aria-haspopup="menu" aria-expanded="false" aria-controls="account-menu">' +
                    '<span class="account-avatar">' + escapeHtml(initials(currentUser.username)) + '</span>' +
                    '<span>' + escapeHtml(currentUser.username) + '</span> ▾</button>' +
                    '<div class="account-menu" id="account-menu" role="menu">' +
                    '<button role="menuitem" ' + act('navigate', 'profile') + '>Profile</button>' +
                    '<button role="menuitem" ' + act('openSettings') + '>Settings</button>' +
                    '<button role="menuitem" ' + act('logout') + '>Logout</button>' +
                    '</div></div>';
```

(The logged-out `else` branch at lines 950–952 is unchanged.)

- [ ] **Step 2: Add helper + handler functions**

Insert these functions in the script body, immediately **before** `function renderHeader()` (line 932):

```javascript
        function initials(name) {
            var s = (name || '').trim();
            if (!s) return '?';
            return s.slice(0, 2).toUpperCase();
        }
        let accountMenuOpen = false;
        function toggleAccountMenu() {
            const menu = document.getElementById('account-menu');
            const chip = document.getElementById('account-chip');
            if (!menu) return;
            accountMenuOpen = !accountMenuOpen;
            menu.classList.toggle('open', accountMenuOpen);
            if (chip) chip.setAttribute('aria-expanded', accountMenuOpen ? 'true' : 'false');
        }
        function closeAccountMenu() {
            if (!accountMenuOpen) return;
            accountMenuOpen = false;
            document.getElementById('account-menu')?.classList.remove('open');
            document.getElementById('account-chip')?.setAttribute('aria-expanded', 'false');
        }
        function openSettings() {
            navigate('dev');
            showDevTab('settings');
        }
```

`openSettings()` is required because there is no top-level `#settings-section`; the user settings UI is the Dev workspace's Settings tab (`showDevTab('settings')` → `loadDevSettings()` → `loadSettings()`). The account dropdown is only rendered when logged in, so `currentUser` is guaranteed set.

- [ ] **Step 3: Add the outside-click + Esc close listener**

Immediately **after** the existing feed-close listener (the block ending at line 4073, `});`), add:

```javascript
        // Close account menu on outside click (item clicks also close it & still fire their action)
        document.addEventListener('click', (e) => {
            if (!accountMenuOpen) return;
            if (e.target.closest('#account-chip')) return; // chip toggle handles itself
            closeAccountMenu();
        });
        document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeAccountMenu(); });
```

Why this works: the delegated `act` listener (registered at line 2005, earlier) runs first on any click and invokes the handler (`toggleAccountMenu`, `navigate`, `openSettings`, `logout`). This second listener then closes the menu — except when the chip itself was clicked (the chip's own toggle already set the correct state). Clicking a menu item therefore both runs its action and closes the menu.

- [ ] **Step 4: Build**

Run: `go build -o playmore`
Expected: exits 0, no output.

- [ ] **Step 5: Manual check (logged in)**

Rebuild+run, log in. Expected:
- Right cluster shows search, 🌙, 🔔, then a pill: round initials (e.g. `YU` for "yusyus") + username + ▾.
- Click the chip → dropdown opens with `Profile / Settings / Logout`.
- `Profile` → profile page loads. Re-open chip, `Settings` → Dev workspace opens on the Settings tab (Account/Playback/Security/API Keys/Data/Danger Zone visible). `Logout` → logs out.
- Open the menu, click empty page area → menu closes. Open it, press `Esc` → closes.
- 🔔 bell dropdown still opens/closes; 🌙 still toggles theme.
- Open browser devtools console: no CSP violations, no "missing handler" warnings.

- [ ] **Step 6: Commit**

```bash
git add frontend/index.html
git commit -m "feat(nav): account chip with initials avatar + Profile/Settings/Logout dropdown"
```

---

## Task 3: Game detail page CSS

Goal: add the classes the new game-detail markup (Task 4) will use, and make the sidebar sticky. CSS-only — no visual change until Task 4 uses the classes, so the app stays working.

**Files:**
- Modify: `frontend/index.html` — append to `.game-*` CSS group (near line 231–238); add a mobile override at the existing `.game-layout { grid-template-columns: 1fr; }` breakpoint (line 464).

- [ ] **Step 1: Add the new game-detail classes and make the sidebar sticky**

Replace the existing `.game-sidebar` rule at line 231:

```css
        .game-sidebar { display: flex; flex-direction: column; gap: 15px; }
```

with (adds sticky positioning) plus the new classes:

```css
        .game-sidebar { display: flex; flex-direction: column; gap: 15px; position: sticky; top: 70px; max-height: calc(100vh - 80px); overflow-y: auto; }
        .detail-header { margin-bottom: 20px; }
        .detail-header .breadcrumbs { margin-bottom: 8px; }
        .detail-title { font-size: 28px; font-weight: bold; line-height: 1.2; margin-bottom: 6px; }
        .detail-by { color: var(--text-secondary); font-size: 14px; }
        .detail-by a { color: var(--accent); cursor: pointer; }
        .detail-capsule { width: 100%; border-radius: 6px; display: block; box-shadow: 0 4px 16px rgba(0,0,0,0.4); }
        .play-box { display: flex; flex-direction: column; gap: 8px; background: rgba(0,0,0,0.2); border-radius: 8px; padding: 12px; }
        .play-box button { width: 100%; }
        .review-summary-compact { display: flex; flex-direction: column; gap: 2px; background: rgba(0,0,0,0.2); border-radius: 8px; padding: 12px; text-align: center; }
        .review-summary-compact-meta { color: var(--text-secondary); font-size: 12px; }
```

- [ ] **Step 2: Disable sticky sidebar in the one-column mobile layout**

Find the mobile rule at line 464:

```css
            .game-layout { grid-template-columns: 1fr; }
```

Replace it with (adds a static-position override so the sidebar flows below the media on narrow screens):

```css
            .game-layout { grid-template-columns: 1fr; }
            .game-sidebar { position: static; max-height: none; overflow: visible; }
```

- [ ] **Step 3: Build**

Run: `go build -o playmore`
Expected: exits 0, no output.

- [ ] **Step 4: Manual check (no regression)**

Rebuild+run, open any game detail page. Expected: page looks the same as before this task (classes are defined but not yet used), EXCEPT the existing sidebar (info box / collection / tags) now stays pinned while you scroll the reviews. No layout breakage at desktop or narrow widths.

- [ ] **Step 5: Commit**

```bash
git add frontend/index.html
git commit -m "feat(game-detail): add Steam-rail CSS (detail header, play box, sticky sidebar)"
```

---

## Task 4: Restructure the game detail layout

Goal: replace the crammed top action-bar + duplicate header block with a clean breadcrumb+title; move Play/Library/Wishlist and the review summary into the sidebar as the Play box. Reuse the existing `2fr/1fr` grid, media gallery, info box, collection, and tags untouched.

**Files:**
- Modify: `frontend/index.html` — `showGameDetail()`: replace lines 2064–2106 (top region); modify line 2243 (sidebar open) to insert capsule + Play box + review summary.

- [ ] **Step 1: Replace the top region (breadcrumb/title)**

Replace lines 2064–2106 — everything from `let html = '<div class="game-detail">';` up to and including the closing `html += '</div>';` that ends the logged-out side-action block (the line immediately before `html += '<div class="game-layout"><div>';`) — with:

```javascript
                let html = '<div class="game-detail">';

                // Breadcrumb + title (full width, above the two-column layout)
                html += '<div class="detail-header">';
                html += '<div class="breadcrumbs"><a ' + act('navigate', 'store') + '>All Games</a> &gt; <a ' + act('navigate', 'store') + '>' + escapeHtml(genreCap) + '</a> &gt; ' + escapeHtml(game.title) + '</div>';
                html += '<h1 class="detail-title">' + escapeHtml(game.title) + '</h1>';
                html += '<div class="detail-by">by <a ' + act('showDeveloper', game.developer_name) + '>' + escapeHtml(game.developer_name) + '</a> &middot; ' + escapeHtml(genreCap) + '</div>';
                html += '</div>';
```

The next existing line (`html += '<div class="game-layout"><div>';`) and the entire left column (media gallery, About, System Requirements, Customer Reviews) remain unchanged.

- [ ] **Step 2: Insert capsule + Play box + review summary into the sidebar**

Find line 2243:

```javascript
                html += '</div><div class="game-sidebar">';
```

Replace it with (opens the sidebar, then adds capsule → Play box → review summary; the existing WebGPU warning / info box / collection / tags code that follows stays put):

```javascript
                html += '</div><div class="game-sidebar">';

                // Capsule cover
                if (coverSrc) html += '<img class="detail-capsule" src="' + esc(coverSrc) + '" alt="">';

                // Play box (primary actions)
                html += '<div class="play-box">';
                html += '<button class="btn-primary" ' + act('playGame', game.id, game.entry_file) + '>▶ Play Now</button>';
                if (currentUser) {
                    html += '<button class="btn-secondary" id="lib-btn-' + game.id + '" ' + act('toggleLib', game.id) + '>＋ Add to Library</button>';
                    html += '<button class="btn-wishlist" id="wish-btn-' + game.id + '" ' + act('toggleWish', game.id) + '>♡ Wishlist</button>';
                } else {
                    html += '<button class="btn-secondary" ' + act('showAuth', 'login') + '>Log in to save</button>';
                }
                html += '</div>';

                // Compact review summary
                if (allReviews.length > 0) {
                    const revLabelSb = posPct >= 90 ? 'Overwhelmingly Positive' : posPct >= 70 ? 'Very Positive' : posPct >= 50 ? 'Mixed' : 'Negative';
                    const revColorSb = posPct >= 70 ? 'var(--success)' : posPct >= 50 ? 'var(--warning)' : 'var(--danger)';
                    html += '<div class="review-summary-compact"><span style="color:' + revColorSb + ';font-weight:bold;">' + revLabelSb + '</span><span class="review-summary-compact-meta">' + allReviews.length + ' reviews &middot; ' + posPct + '% positive</span></div>';
                }
```

Notes:
- `coverSrc`, `allReviews`, `posPct` are already computed earlier in `showGameDetail()` (lines 2058–2061). `esc()` (attribute-safe) is the same helper used by the original cover `<img>`.
- The `id="lib-btn-<id>"` and `id="wish-btn-<id>"` are preserved exactly, so the post-render status sync at lines 2293–2303 still finds and updates them.
- The original top-region also rendered these buttons; Step 1 removed that block, so there is no duplication.

- [ ] **Step 3: Build**

Run: `go build -o playmore`
Expected: exits 0, no output.

- [ ] **Step 4: Manual check (logged in)**

Rebuild+run, log in, open a game detail page (one that has reviews and a cover). Expected:
- Breadcrumb on a single line at the top (NOT stacked vertically), then the title, then `by <dev> · <genre>`.
- Left column: media gallery, then About / System Requirements / Customer Reviews.
- Right sidebar, top to bottom: capsule cover image → Play box (`▶ Play Now`, `＋ Add to Library`, `♡ Wishlist`, all full-width) → "Very Positive · N reviews · N% positive" → existing info box → `+ Add to Collection` → tags.
- `▶ Play Now` launches the game. `＋ Add to Library` toggles and its label updates; reload the page → it reflects saved state (`Remove from Library`). Same for Wishlist (`On Wishlist ✓`).

- [ ] **Step 5: Manual check (logged out + edge cases)**

- Logged out: Play box shows `▶ Play Now` + `Log in to save` (no Library/Wishlist).
- A game with **no screenshots/video**: capsule still shows in the sidebar; the media area falls back to the cover (existing behavior).
- A game with **no reviews**: the compact review summary is omitted (no empty box).
- A WebGPU game in a non-WebGPU browser: the WebGPU warning still appears in the sidebar.
- Narrow viewport: layout collapses to one column, sidebar (with Play box) drops below the media, breadcrumb still on one line.
- Console: no CSP violations, no missing-handler warnings.

- [ ] **Step 6: Commit**

```bash
git add frontend/index.html
git commit -m "feat(game-detail): Steam two-column layout with sidebar Play box"
```

---

## Task 5: Full regression pass against the spec checklist

Goal: confirm the whole spec is satisfied end-to-end before finishing the branch.

**Files:** none (verification only).

- [ ] **Step 1: Run the spec's manual test checklist**

Rebuild+run (`go build -o playmore && ./playmore`) and walk every item in the design spec's "Testing" section (`docs/superpowers/specs/2026-06-15-nav-and-game-detail-redesign-design.md`): nav logged-in/out, account dropdown (Profile/Settings/Logout, outside-click, Esc), theme icon, bell, game detail logged-in/out, no-media game, no-review game, WebGPU warning, sticky rail, responsive collapse, and a clean console (no CSP violations).

Expected: every item passes. If any fails, fix in `frontend/index.html`, rebuild, re-check, and commit the fix with a `fix(...)` message before proceeding.

- [ ] **Step 2: Confirm the build is clean**

Run: `go build -o playmore`
Expected: exits 0, no output.

- [ ] **Step 3: Finish the branch**

Use the superpowers:finishing-a-development-branch skill to decide how to integrate `feature/nav-game-detail-redesign` (merge / PR / cleanup).

---

## Self-review notes

- **Spec coverage:** Nav Option 1 (grouping + spacer = Task 1; chip + dropdown + initials + theme-stays-icon = Task 2). Game Option A (breadcrumb/title = Task 4 Step 1; capsule + Play box + review summary in sidebar, info/collection/tags retained = Task 4 Step 2; sticky rail + mobile override = Task 3). Logged-out states, no-media/no-review edges, WebGPU warning, responsive, CSP, accessibility (aria on chip/menu, Esc) all covered in Task 2/4 checks. Manual-only testing acknowledged up front per `CLAUDE.md`.
- **Settings target:** routed via `openSettings()` → `navigate('dev'); showDevTab('settings')` because no `#settings-section` exists; verified `loadDevSettings()` → `loadSettings()` is the real settings UI.
- **No-regression ordering:** Task 1 leaves logged-in header working (old Welcome/Logout) until Task 2 swaps it; Task 3 is CSS-only (classes unused) so nothing breaks before Task 4 consumes them.
- **Identifier consistency:** `account-chip`, `account-menu`, `accountMenuOpen`, `toggleAccountMenu`, `closeAccountMenu`, `openSettings`, `initials`, `.play-box`, `.detail-header`, `.detail-title`, `.detail-by`, `.detail-capsule`, `.review-summary-compact` are used identically across CSS, markup, and handlers. Preserved IDs `lib-btn-<id>`/`wish-btn-<id>` match the existing status-sync code (2293–2303).
