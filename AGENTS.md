# AGENTS.md

This file provides guidance for AI coding agents working with the PlayMore codebase.

## Project Overview

PlayMore is a **single-file web application** (`index.html`, ~2460 lines) — a self-hosted game publishing platform UI modeled after Steam. It is a completely self-contained HTML/CSS/JS app with **no build system, no dependencies, and no package manager**.

## Technology Stack

- **Frontend**: Pure HTML5, CSS3, and vanilla JavaScript (ES6+)
- **No frameworks**: No React, Vue, Angular, or any JS libraries
- **No build tools**: No webpack, Vite, Rollup, or transpilers
- **No package manager**: No `package.json`, `node_modules`, `pyproject.toml`, `Cargo.toml`, etc.
- **Persistence**: Browser `localStorage` (key: `playmore_library`)
- **External assets only**: Unsplash images, DiceBear avatars, YouTube embeds

## Project Structure

```
/mnt/1ece809a-2821-4f10-aecb-fcdf34760c0b/Git/playmore/
├── index.html   # The entire application (CSS + HTML + JS in one file)
├── CLAUDE.md    # Guidance for Claude Code
└── AGENTS.md    # This file
```

Everything lives inside `index.html` in three inline sections:

1. **CSS (~1300 lines)** inside `<style>` — custom properties in `:root`, Steam-like dark theme, responsive layout, animations.
2. **HTML (~400 lines)** inside `<body>` — tab-based SPA with 5 sections: Home (Store), Game Detail, Library, Profile, Upload.
3. **JavaScript (~750 lines)** inside `<script>` — all app logic, data, and event handlers.

## Running the Application

Open `index.html` directly in a browser. The `file://` protocol works fine. No server is required.

```bash
# Optional: serve locally if you prefer http://localhost
python -m http.server 8000
```

## Architecture & Code Organization

### Tab-Based SPA Navigation

- Navigation uses `data-tab` attributes and `switchTab(tabName)` to toggle `.active` classes on `.nav-tab` and `.section` elements.
- Sections: `home-section`, `game-detail-section`, `library-section`, `profile-section`, `upload-section`.
- **Important**: `showHome()` resets the genre filter. If you need to navigate to Home with a genre pre-selected, call `switchTab('home')` directly instead of `showHome()`.

### Key Data Structures

- `gamesData` — static catalog object keyed by game slug. Contains 4 games: `neon-overdrive`, `void-echoes`, `shadow-tactics`, `xox-classic`.
- `reviewsData` — static review arrays keyed by game slug.
- `library` — array of game IDs persisted to `localStorage` under key `playmore_library`.
- `currentGameId` / `currentGenreFilter` — simple navigation state variables.

### Key UI Flows

- **Store → Game Detail**: `showGameDetail(gameId)` populates DOM elements by ID, renders the media carousel, and injects reviews.
- **Game Detail → Play**: `playCurrentGame()` → `playGame(gameId)` opens a fullscreen overlay with an `<iframe>`.
- **Genre filtering**: `filterByGenreFromDetail()` navigates to the home tab with the genre pre-selected. `filterGames()` handles search, genre, and sort logic.
- **Library**: On first visit, all games are auto-added to the library. Library cards click through to game detail; the Play button opens the game player.

### Game Player

- The player is a fixed-position overlay (`#game-player`) that uses CSS transitions (`opacity` and `transform`) for smooth fade in/out.
- **Do not use `display:none` toggling** for the player overlay. It relies on `pointer-events: none` + `opacity: 0` by default, and adding `.active` enables `pointer-events: all` + `opacity: 1`.
- `xox-classic` is a fully self-contained HTML tic-tac-toe game injected as a Blob URL into an iframe with `sandbox="allow-scripts allow-same-origin"`.
- Other games show a placeholder demo spinner inside the iframe.

### Visual Design System

Custom CSS properties in `:root`:

```css
--bg-primary: #1b2838;
--bg-secondary: #171a21;
--bg-tertiary: #2a475e;
--accent: #66c0f4;
--accent-hover: #417a9b;
--text-primary: #c7d5e0;
--text-secondary: #8f98a0;
--success: #a1cd44;
--warning: #d2a960;
--danger: #c15757;
--price: #beee11;
--discount-bg: #4c6b22;
--card-bg: rgba(0, 0, 0, 0.3);
--border-color: rgba(255, 255, 255, 0.1);
```

- Desktop-first, responsive down to 1366x768.
- Uses CSS Grid for game listings, library cards, and system requirements.
- Background particle animation is rendered on a `<canvas id="particles-canvas">`.

## Development Conventions

- **Single-file constraint**: All new features, styles, and logic must be added inline to `index.html`. Do not create additional files unless explicitly requested.
- **Vanilla JS only**: No importing npm packages or CDN libraries.
- **Inline styles/scripts**: CSS goes in the `<style>` block; JS goes in the `<script>` block at the bottom of `<body>`.
- **DOM manipulation**: Uses standard `document.getElementById`, `querySelector`, `innerHTML`, and event listener patterns.
- **No modules**: Everything is in the global scope.

## Testing

There is no test suite, test runner, or testing framework. Manual testing is done by opening `index.html` in a browser and interacting with the UI.

## Deployment

Since the app is a single static HTML file, deployment is trivial:

- Copy `index.html` to any static web host (GitHub Pages, Netlify, Vercel, S3, etc.).
- No build step or environment variables are needed.

## Security Considerations

- The app uses `localStorage` for minimal client-side persistence (library state only). No sensitive data is stored.
- The game player iframe uses `sandbox="allow-scripts allow-same-origin"` for Blob URL content.
- External images and embeds are loaded over HTTPS from Unsplash, DiceBear, and YouTube.
