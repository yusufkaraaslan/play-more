# Authentication

PlayMore uses four credential types. Games running in the PlayMore iframe use short-lived session tokens (`pm_gs_`); server-side integrations use long-lived SDK keys (`pm_gk_`). The SPA and user-facing tools use session cookies or user API keys (`pm_k_`).

## Credential types

| Credential | Prefix | Scope | Lifetime | Auth method |
|---|---|---|---|---|
| Session cookie | (none) | User account | 30 days | `session` |
| User API key | `pm_k_` | User account | Long-lived, revocable | `api_key` |
| Game SDK key | `pm_gk_` | Single game | Long-lived, revocable | `game_api_key` |
| Game session token | `pm_gs_` | Single game | 5 minutes | `game_session` |

All Bearer tokens are passed in the `Authorization` header:

```
Authorization: Bearer pm_gs_<token>
```

### Session cookie (SPA)

The PlayMore SPA authenticates with a `session` HTTP-only cookie set on login. This is the only credential that carries cookies — it never crosses origins. The SPA's same-origin `fetch()` calls use it automatically.

### User API key (`pm_k_`)

Long-lived, user-scoped keys created from **Settings → API Keys**. Can call any endpoint the user can access, including account endpoints. Useful for CLI tools, scripts, and CI that act on behalf of a user.

### Game SDK key (`pm_gk_`)

Long-lived, **game-scoped** keys. Created by the game's developer from **Settings → Game SDK Keys** (or via the API):

```bash
curl -X POST https://playmore.example.com/api/v1/games/<game-id>/sdk-keys \
  -H "Authorization: Bearer pm_k_<your-api-key>" \
  -H "Content-Type: application/json" \
  -d '{"name": "CI deploy key", "scopes": "all"}'
```

Response (the raw key is shown **once**):

```json
{
  "key": {
    "id": "uuid",
    "game_id": "...",
    "name": "CI deploy key",
    "key_prefix": "pm_gk_a1b2c3d4",
    "scopes": "all",
    "created_at": "2026-07-08T12:00:00Z"
  },
  "raw_key": "pm_gk_<64 hex chars>",
  "message": "Copy this key now. You won't be able to see it again."
}
```

**Use cases:**
- Server-side game logic (leaderboards, match orchestration)
- CI/CD pipelines (deploy builds, manage versions)
- Backend services that act on behalf of a specific game

**Limitations:**
- Maximum 5 keys per game
- Cannot access account endpoints (`/auth/*`, `/settings/*`, `/api-keys`, `/profile`, `/admin/*`) → **403**
- Cannot connect to the WebSocket (`/ws`) → **403**
- Scopes: `"all"` grants everything; specific scopes can be CSV-listed. Only `session:write` gates behavior currently; other scopes are reserved.

Manage keys:

```bash
# List keys for a game
curl https://playmore.example.com/api/v1/games/<game-id>/sdk-keys \
  -H "Authorization: Bearer pm_k_<your-api-key>"

# Revoke a key
curl -X DELETE https://playmore.example.com/api/v1/games/<game-id>/sdk-keys/<key-id> \
  -H "Authorization: Bearer pm_k_<your-api-key>"
```

### Game session token (`pm_gs_`)

Short-lived (5-minute TTL), game-scoped runtime tokens. These are the credential games use inside the PlayMore iframe.

## How the game iframe gets a session token

The SPA (parent page) mints the token and passes it to the game iframe via `postMessage`:

1. The SPA calls `POST /api/v1/games/:id/sdk-token` with the user's session cookie.
2. The server returns a `pm_gs_` token bound to the user + game.
3. The SPA sends an `init` postMessage to the iframe with `session_token` included.
4. The game receives it via `PlayMore.onReady()`:

```javascript
PlayMore.onReady(function (ctx) {
  // ctx.sessionToken === "pm_gs_..."
  // ctx.gameId === "..."
  // ctx.you === { id, username }
});
```

Any authenticated user can mint a token for a **published** game (they're a player). Only the game's developer can mint for an unpublished game (for testing).

## What session tokens can do

`pm_gs_` tokens can call game-scoped endpoints:

- **Play sessions**: open, heartbeat, end (`/games/:id/play-sessions`, `/play-sessions/:sid/*`)
- **WebSocket**: connect to `/ws?token=pm_gs_...` (browsers can't set headers on WS handshakes, so the token goes in the query string)
- **Game-scoped reads**: game detail, reviews, etc.

## What session tokens cannot do

Account and credential endpoints reject game-scoped credentials with **403**:

```
{ "error": "this credential cannot access account endpoints" }
```

Blocked surfaces:
- `/api/v1/auth/*` — login, register, logout, password reset
- `/api/v1/settings/*` — account deletion, password change
- `/api/v1/api-keys/*` — user API key management
- `/api/v1/profile` — profile updates
- `/api/v1/admin/*` — admin operations
- `/api/v1/seed` — demo data seeding

## Using the session token for API calls

Pass the token as a Bearer token in the `Authorization` header:

```javascript
// Inside the game iframe — call a game-scoped API.
const token = PlayMore.sessionToken();

async function openPlaySession(gameId) {
  const res = await fetch(`/api/v1/games/${gameId}/play-sessions`, {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${token}`
    }
  });
  return res.json();
}
```

For WebSocket connections, the token goes in the query string (browsers don't allow custom headers on WS handshakes):

```javascript
const ws = new WebSocket(`/ws?token=${PlayMore.sessionToken()}`);
```

The `WSQueryTokenAuth` middleware copies the `?token=` parameter into the `Authorization` header so the standard auth pipeline processes it.

## CORS

Game iframes run in a sandboxed context (`sandbox` without `allow-same-origin`), which makes them **opaque-origin** — every `fetch()` from the game carries `Origin: null`. A CORS policy that echoes a specific origin would reject these legitimate calls.

PlayMore's API returns `Access-Control-Allow-Origin: *` on game-facing API paths. This is safe because:

- Game-facing auth is **exclusively Bearer** (`pm_gk_` / `pm_gs_`), never cookies.
- Bearer tokens are not auto-attached by the browser, so CSRF attackers without the raw token cannot complete calls.
- `credentials: 'include'` is never used from the game iframe.

**Excluded from the cross-origin surface** (SPA-shell only, same-origin cookie auth):

- `/api/v1/auth/*`
- `/api/v1/admin/*`
- `/api/v1/settings/*`
- `/api/v1/api-keys*`
- `/api/v1/profile*`
- `/api/v1/seed`

These paths receive no CORS headers — a game iframe cannot call them.

## Security

| Property | Session token (`pm_gs_`) | SDK key (`pm_gk_`) |
|---|---|---|
| TTL | 5 minutes (hard expiry) | Long-lived until revoked |
| Scope | Single game | Single game |
| Max active | 20 per user | 5 per game |
| Revocable | Yes (single `UPDATE`) | Yes (delete) |
| Storage | SHA-256 hash only (raw shown once) | SHA-256 hash only (raw shown once) |
| Entropy | 256 bits (crypto/rand) | 256 bits (crypto/rand) |

Session tokens are designed for short blast radius: the SPA refreshes them before expiry (every ~4 minutes), and expired or revoked tokens immediately stop working.
