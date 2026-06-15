# PlayMore — Global Nav & Game Detail Page Redesign

**Date:** 2026-06-15
**Scope:** Frontend only (`frontend/index.html`). No Go, API, or database changes.
**Status:** Approved design, ready for implementation plan.

## Problem

Two parts of the UI feel crowded and unpolished:

1. **Game detail page.** The breadcrumb and the `Play Now / Library / Wishlist`
   buttons are jammed into a single flex row (`index.html:2067–2074`). The
   breadcrumb (`flex:1`) gets squeezed to a narrow column and its text wraps
   into a vertical stack on the left, while the buttons crowd the rest of the
   row. A separate cover+title+reviews header block (`2077–2106`) duplicates
   metadata that also lives in the sidebar.

2. **Global top nav.** The header uses `justify-content: space-between` across
   five loose children — logo, nav tabs, a `🌙` theme toggle, the search box,
   and a `header-actions` cluster (`🔔` bell, "Welcome, username", Logout)
   (`index.html:518–538`, dynamic actions at `940–949`). The items float apart
   with no grouping, and the theme toggle sits awkwardly mid-bar.

The reference target is a Steam game page (clean breadcrumb, big media left,
info/action rail right) and a grouped Steam-style nav. PlayMore's accent color
is already Steam blue (`rgba(102,192,244)`, `#66c0f4`), so this is primarily a
layout/grouping change, not a re-theme.

Key adaptation: **PlayMore games are free and instant-play** (no price, no cart,
no purchase). Steam's right rail is built around buying; ours becomes a
**Play box** instead.

## Goals

- Un-cram the game detail top region; adopt a Steam-style two-column page where
  the right sidebar is the action + info rail.
- Group the global nav into a left cluster (brand + primary tabs) and a right
  cluster (search, theme, notifications, account), with an account dropdown.
- Reuse existing structure, CSS, and event handlers wherever possible.
- Preserve CSP safety: no inline `on*=` handlers; use the existing
  `act()` / `onEv()` delegated-event pattern.

## Non-goals

- No backend, API, or schema changes.
- No user avatars/images (use text initials from `username`).
- No new pages or routes; `Profile` and `Settings` already exist as navigate
  targets (`index.html:1126`).
- No redesign of Store grid, Library, Feed, Dev dashboard, or other tabs beyond
  what the shared nav change touches.
- No change to media-gallery internals (lightbox, video posters, thumb nav).

## Design

### 1. Global nav (Option 1 — grouped + account dropdown)

**Structure**, left to right:

- **Left group:** logo + primary tabs `Store · Library · Feed · Dev`.
  - `Profile` is **removed from the tab row** and moves into the account
    dropdown. `Feed` remains conditionally shown (`id="nav-feed"`, logged-in).
- **Flex spacer** (`flex:1`) separating left and right groups.
- **Right group:** search box → `🌙` theme toggle (icon, one-click, stays as
  today) → `🔔` notifications (existing `feed-btn` + dropdown) → **account chip**.

**Account chip** (replaces the loose `Welcome, <username>` + `Logout`):

- A pill: round initials avatar + username + `▾` caret.
- Initials: derived from `currentUser.username` — first two characters
  uppercased (e.g. `yusyus` → `YU`). Helper `initials(name)`.
- Click toggles a dropdown menu containing: **Profile** (`navigate('profile')`),
  **Settings** (`navigate('settings')`), **Logout** (`logout`).
- New handler `toggleAccountMenu()`; closes on outside click (mirror the
  existing pattern used by `feed-btn`/`feed-dropdown` and global-search at
  `index.html:4069`, `4371`).
- Logged-out state is unchanged: `Login` + `Register` buttons.

**Layout mechanism:** restructure the static `<header>` so the right-side
controls live in a single grouped container, and the nav/search/actions are
separated by a spacer rather than `space-between` across all children. The
theme toggle and search move into the right group.

**CSS:** add `.account-chip`, `.account-avatar`, `.account-menu`
(dropdown), reusing existing dropdown visual conventions (`.feed-dropdown`).
The bell, theme, and chip align in the right group with consistent gaps
(`.header-actions` already provides `display:flex; align-items:center; gap`).

### 2. Game detail page (Option A — Steam two-column)

The page already uses `.game-layout { grid-template-columns: 2fr 1fr }`
(`index.html:197`) that collapses to one column on mobile (`464`). The work is
to **relocate** the actions and review summary into the existing
`.game-sidebar`, and simplify the top.

**Removed:**

- The crammed top action-bar row (`index.html:2067–2074`).
- The cover + title + review-summary + tags header block (`2077–2106`),
  including the logged-out side action column.

**Top region (new, full width, above `.game-layout`):**

- Breadcrumb on its own line (`All Games › Genre › Title`).
- `H1` game title.
- `by <Developer> · <Genre>` subline (developer links to `showDeveloper`).

**Left column (`2fr`) — unchanged order:** media gallery → About This Game →
System Requirements → Customer Reviews → (developer's devlog section if
present). The cover-as-fallback behavior when there is no media is retained
(`index.html:2115`).

**Right sidebar (`1fr`, `.game-sidebar`) — new top-to-bottom order:**

1. **Capsule cover image** at the top of the rail (the cover currently rendered
   at `2079`). Falls back to a placeholder block when absent.
2. **Play box** (new container, e.g. `.play-box`):
   - Logged in: `▶ Play Now` (full-width primary green, `playGame`),
     `＋ Add to Library` (`toggleLib`, id `lib-btn-<id>`),
     `♡ Wishlist` (`toggleWish`, id `wish-btn-<id>`).
   - Logged out: `▶ Play Now` + `Log in to save` (`showAuth('login')`).
   - Button IDs and the post-render status checks (`lib-btn-*`, `wish-btn-*`
     at `index.html:2293–2303`) are preserved so library/wishlist state still
     updates correctly.
3. **Review summary** (compact): label ("Very Positive" etc.) + "N reviews ·
   N% positive". Reuses the `posPct`/`revLabel`/`revColor` logic already
   computed at `2060–2061, 2086–2087`. The detailed star-distribution summary
   stays in the Customer Reviews section in the left column.
4. **Info box** (Developer, Genre, Released, Graphics, Size) — already exists
   (`2257–2264`), unchanged.
5. **+ Add to Collection** button (`2265–2267`) and **tags** (`2268`) —
   unchanged.
6. **WebGPU compatibility warning** (`2246–2254`) — unchanged; keep near top of
   rail if the game requires WebGPU and the browser lacks it.

**Sticky rail:** `.game-sidebar { position: sticky; top: ~70px }` (the header is
`60px` tall and `sticky`, `index.html:60`, plus a small gap), with a
`max-height: calc(100vh - 80px)` + `overflow-y:auto` fallback so a tall rail
never traps content. Disabled in the single-column mobile breakpoint.

**Responsive:** `.game-layout` already collapses to `1fr` at `index.html:464`.
In one-column mode the sidebar renders below the media; verify the Play box
lands high enough to be reachable without excessive scrolling (acceptable since
the title/breadcrumb above it are short).

## Implementation notes

- **Single file:** `frontend/index.html` (static `<header>` markup ~`518–538`,
  dynamic header actions in `renderHeader` ~`940–949`, `showGameDetail`
  ~`2052–2306`, and the CSS block for `.header`/`.game-*`).
- **Event safety (CSP):** all new clicks use `act('fn', ...args)`; non-click
  events use `onEv(...)`. No inline `on*=`. New global functions
  (`toggleAccountMenu`, `initials` is a pure helper) attach to `window` like the
  existing handlers so the delegated dispatcher finds them.
- **Reused handlers:** `playGame`, `toggleLib`, `toggleWish`, `navigate`,
  `toggleTheme`, `logout`, `showAuth`, `showDeveloper`, `showAddToListModal`.
- **New CSS classes:** `.account-chip`, `.account-avatar`, `.account-menu`,
  `.play-box`, `.detail-header` (breadcrumb/title block). Reuse `.info-box`,
  `.btn-primary`, `.btn-secondary`, `.btn-wishlist`, `.tag`, `.review-summary`.
- **Outside-click close:** extend the existing document click listener that
  already closes `feed-dropdown` and `global-search` results to also close
  `account-menu`.

## Accessibility

- Account chip: `aria-haspopup="menu"`, `aria-expanded`, `aria-controls`;
  dropdown items are buttons/links reachable by keyboard; `Esc` closes (match
  existing dropdown behavior where present).
- Nav tabs keep `role="navigation"`, `tabindex`, and `aria-current` handling
  (`index.html:1090, 1135`).
- Initials avatar is decorative; the username text label carries the name.
- Maintain visible focus styles; hover-only affordances also respond to focus.

## Testing (manual — no automated suite per CLAUDE.md)

Build (`go build -o playmore`), seed demo data (`POST /api/seed`), then in the
browser verify:

1. **Nav, logged in:** left group (logo + Store/Library/Feed/Dev), right group
   (search, theme, bell, chip). Chip shows correct initials + username.
2. **Account dropdown:** opens on click, Profile/Settings/Logout navigate
   correctly, closes on outside click and `Esc`. Theme toggle still works as a
   one-click icon. Bell dropdown still works.
3. **Nav, logged out:** Login/Register buttons; no chip.
4. **Game detail, logged in:** breadcrumb on one line (no vertical stacking),
   title + by-line, media gallery left, sidebar rail right with capsule → Play
   box → review summary → info → collection → tags. Play Now launches; Library
   and Wishlist toggle and reflect state on reload.
5. **Game detail, logged out:** Play Now + "Log in to save" in the Play box.
6. **Game with no media:** cover used as media fallback; capsule still shows.
7. **WebGPU game on non-WebGPU browser:** warning appears in the rail.
8. **Sticky rail:** sidebar stays in view while scrolling long reviews.
9. **Responsive:** narrow viewport collapses to one column; nav stays usable
   (existing mobile breakpoints at `464–499`); Play box reachable.
10. **CSP:** no console CSP violations; no inline handler errors.

## Out of scope / future

- Real uploaded avatars (chip is initials-only for now).
- Two-tier nav (Option 3) and hero-banner page (Option B) were considered and
  declined.
- Any restructuring of Store/Library/Feed/Dev page bodies.
