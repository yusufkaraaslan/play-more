# Play Sessions

Play sessions are the live game session ledger. Each open session records who is playing which game, and heartbeats keep it alive. Active sessions drive the "online players" metric shown on game pages and in admin analytics.

## The API

All endpoints are mounted under both `/api/v1/` (canonical) and `/api/` (alias).

### Open a session

```
POST /api/v1/games/:id/play-sessions
```

Creates a new play session for the authenticated user + game. Returns the session record.

```json
{
  "session_id": "uuid",
  "user_id": "...",
  "game_id": "...",
  "started_at": "2026-07-08T12:00:00Z",
  "last_heartbeat": "2026-07-08T12:00:00Z"
}
```

**Rate limit:** 30 requests / 60 seconds.

### Heartbeat

```
POST /api/v1/play-sessions/:sid/heartbeat
```

Updates `last_heartbeat` for the session. Ownership is enforced in the SQL (`user_id` must match). Returns 404 if the session doesn't exist or has ended.

```json
{ "status": "ok" }
```

**Rate limit:** 12 requests / 60 seconds (one every 5s; the SPA sends every 30s).

### End a session

```
POST /api/v1/play-sessions/:sid/end
```

Marks the session as ended (`ended_at` set). Idempotent — ending an already-ended session is a no-op.

```json
{ "status": "ended" }
```

**Rate limit:** 10 requests / 60 seconds.

## Authentication

Play session endpoints accept:

- **Session cookie** — the SPA calls these directly on behalf of the player.
- **`pm_gs_` game session token** — the game iframe can call directly via CORS.

When authenticating with a `pm_gs_` token, the token's `game_id` must match the game in the URL path. A token minted for game A cannot open sessions for game B → **403**.

```json
{ "error": "token not valid for this game" }
```

`pm_gk_` SDK keys are **not** accepted on these endpoints (they're for server-side logic, not browser sessions).

## How the SPA manages sessions

The PlayMore SPA handles the full lifecycle automatically:

1. **Open** — when the player launches a game, the SPA opens a play session.
2. **Heartbeat** — every 30 seconds while the game is running, the SPA sends a heartbeat.
3. **End** — when the player closes the game tab or navigates away, the SPA ends the session.

The SPA uses the user's session cookie for these calls. Game developers don't need to do anything — this is automatic.

## Online players metric

A session counts as "active" (online) when:

- `last_heartbeat` is within the last **5 minutes**, AND
- `ended_at IS NULL`

The game detail API returns `online_players` for the game page badge. Admin analytics aggregates this across all games for the realtime player count.

Stale sessions (heartbeat older than 24 hours) are cleaned up hourly.

## Calling from the game iframe

Games can also manage play sessions directly using the `pm_gs_` session token. This is useful if your game needs to track its own session lifecycle (e.g., custom heartbeat logic, or the SPA's session was lost).

The session token is available via `PlayMore.sessionToken()` after `onReady`:

### Example: heartbeat from a game iframe

```javascript
PlayMore.onReady(function (ctx) {
  const token = ctx.sessionToken;
  const gameId = ctx.gameId;
  let sessionId = null;
  let heartbeatInterval = null;

  // Open a play session when the game starts.
  fetch('/api/v1/games/' + gameId + '/play-sessions', {
    method: 'POST',
    headers: { 'Authorization': 'Bearer ' + token }
  })
    .then(function (r) { return r.json(); })
    .then(function (session) {
      sessionId = session.session_id;

      // Heartbeat every 30 seconds while playing.
      heartbeatInterval = setInterval(function () {
        if (!sessionId) return;
        fetch('/api/v1/play-sessions/' + sessionId + '/heartbeat', {
          method: 'POST',
          headers: { 'Authorization': 'Bearer ' + token }
        }).catch(function () {});
      }, 30000);
    });

  // End the session when the match ends (game-level event).
  // The SPA also ends the session on tab close — this is for
  // in-game events like "match over" or "return to menu".
  function endSession() {
    if (heartbeatInterval) clearInterval(heartbeatInterval);
    if (!sessionId) return;
    fetch('/api/v1/play-sessions/' + sessionId + '/end', {
      method: 'POST',
      headers: { 'Authorization': 'Bearer ' + token }
    }).then(function () { sessionId = null; });
  }
});
```

> **Note:** The SPA already opens, heartbeats, and ends a session automatically. Calling these endpoints from the game is supplementary — useful when the game wants its own session for custom analytics or needs finer control over the heartbeat cadence.
