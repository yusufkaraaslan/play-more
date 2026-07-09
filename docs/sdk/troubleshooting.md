# Troubleshooting

Common issues game developers hit when integrating the PlayMore multiplayer SDK. Each entry lists the symptom, the cause, and the fix.

---

## Game doesn't receive messages

**Symptom:** `PlayMore.onMessage` never fires, but `onReady` did.

**Cause:** The most common cause is registering `onMessage` **after** the lobby already started. The `init` frame arrives as soon as the iframe loads — if your `onMessage` handler isn't attached by then, early messages are lost. Less commonly, the lobby may not have been started (the host hasn't clicked start, or not all players are ready).

**Solution:** Register all four callbacks (`onReady`, `onMessage`, `onPlayers`, `onClosed`) before the `init` frame can arrive. The shim posts `{ playmore: 'ready' }` to its parent on load and waits for the `init` response, so as long as your `<script>` tags are in order this is automatic. If you're dynamically attaching handlers, attach them synchronously at the top level, not inside an async callback. To rule out a lobby-not-started issue, check `PlayMore.isActive()` — it returns `true` only after the `init` frame.

---

## "PlayMore is undefined"

**Symptom:** `ReferenceError: PlayMore is not defined` when calling `PlayMore.onReady(...)`.

**Cause:** The script tag is missing, or it's included **after** the code that uses it. The shim assigns `window.PlayMore` at the end of its IIFE, so it must load before your game code runs.

**Solution:** Put the script tag before your game script, and ensure it's served from the same PlayMore instance hosting the game:

```html
<script src="/playmore-mp.js"></script>
<script src="game.js"></script>
```

The shim is a no-op if `window.PlayMore` already exists (`if (window.PlayMore) return;`), so including it twice is harmless.

---

## WebRTC never connects

**Symptom:** `PlayMore.transport(peerId)` always returns `'relay'`. P2P never establishes even on a fast network.

**Cause:** A NAT type or firewall is blocking the WebRTC ICE candidate exchange. Symmetric NAT, corporate firewalls, and some mobile carriers block UDP entirely. Without a TURN server, these peers can't relay through a third party.

**Solution:** This is expected behavior for some environments — the relay fallback is there precisely for this case, and your game code is identical either way. For production, configure a TURN server via `--turn-servers` so peers behind hostile NATs have a relay path. To debug, check `PlayMore.stats()` — a peer with `transport: 'relay'` and low `sent`/`received` counts may have a failed data channel. Inspect the browser's `chrome://webrtc-internals` for ICE candidate gathering failures.

---

## CORS error in console

**Symptom:** `Access to fetch at '...' from origin 'null' has been blocked by CORS policy`.

**Cause:** Game iframes are sandboxed (opaque origin, `Origin: null`). The PlayMore CORS middleware returns `Access-Control-Allow-Origin: *` for API paths, but **excludes** account/credential routes: `/auth/*`, `/admin/*`, `/seed`, `/settings/*`, `/api-keys*`, and `/profile*`. Calling any of these from a game iframe fails. The same error appears if you call a non-API path (e.g. `/health`) expecting JSON.

**Solution:** Only call game-scoped API endpoints from the iframe — the ones that accept a `pm_gs_` session token or `pm_gk_` SDK key (play sessions, SDK token operations). Account management belongs in the SPA shell, which is same-origin. If you need the session token from inside the game, use `PlayMore.sessionToken()` — the SPA passes it in via the `init` frame; don't try to mint one from the iframe.

---

## Token expired

**Symptom:** API call from the game iframe returns 401 "session token expired".

**Cause:** `pm_gs_` tokens have a 5-minute TTL. The SPA refreshes the token every ~4 minutes and passes the new one to the iframe, but if the game cached the raw token string at launch it will be stale within minutes.

**Solution:** Never cache the raw token. Call `PlayMore.sessionToken()` each time you need it — the shim reads from the live context object that the SPA updates on refresh. If the SPA tab is backgrounded (throttled by the browser), the refresh may lag; the token will be rejected until the SPA wakes up and mints a fresh one.

---

## Lobby code doesn't work

**Symptom:** A player enters a lobby code and gets "lobby not found".

**Cause:** Lobby codes are 6 characters, case-insensitive, from an ambiguous-character-free alphabet (no `0`/`O`/`1`/`I`). A code that's too short, too long, or uses a non-alphabet character won't match. The lobby may also be full (8 players) or already started — both reject joins.

**Solution:** Verify the code is exactly 6 characters. Codes are uppercased automatically, so case doesn't matter. If the code is correct, the lobby may have started (joins are blocked once the game launches) or filled. The host can share the code from the lobby UI; players should join before the host starts.

---

## Can't create SDK key

**Symptom:** `POST /api/v1/games/:id/sdk-keys` returns 404 "game not found" even though the game exists.

**Cause:** SDK keys are game-scoped credentials. Only the game's developer can create, list, or delete them. The handler checks ownership (`models.IsGameOwner`) and returns 404 (not 403) to avoid leaking which game IDs exist. You may also hit the per-game cap of 5 keys, which returns 400 "maximum 5 SDK keys per game".

**Solution:** Make sure you're authenticated as the game's developer (session cookie, not a `pm_gs_` token — SDK key management is session-auth only). If you have 5 keys, delete an unused one first. Key names must be unique per game and ≤100 characters.

---

## WebSocket disconnects

**Symptom:** The WebSocket connection drops unexpectedly. `onClosed` fires.

**Cause:** Several possibilities: exceeding the 30 messages/second rate limit (connection is closed), a ping timeout (the server didn't receive a pong within 10s of a 30s ping — mobile networks, sleeping laptops), a server restart (the server broadcasts a `server_restarting` closed frame before shutdown; lobbies are persisted and rejoinable), or the outbound queue overflowing (64 frames backed up — the client wasn't reading).

**Solution:** Check the error frame if one arrived before disconnect — "message rate limit exceeded" tells you to batch state updates. For ping timeouts, ensure the game keeps the WebSocket alive (the shim does this automatically; if you're using a raw client, handle pings). Server restarts are now recoverable: the server broadcasts `server_restarting` before shutting down, and persisted lobbies are restored on startup so former members can rejoin with the same code. Still design your game to tolerate a mid-session disconnect (show a "reconnecting" screen, allow rejoining).

---

## online_players shows 0

**Symptom:** The game page shows 0 online players even though people are playing.

**Cause:** The `online_players` count comes from play sessions, not WebSocket connections. A session counts as active only if its last heartbeat is within 5 minutes. If the SPA didn't open a play session on launch, or stopped sending heartbeats (tab backgrounded, network drop), the count drops to 0.

**Solution:** Ensure the SPA opens a play session (`POST /api/v1/games/:id/play-sessions`) when the game launches and heartbeats every 30s (`POST /api/v1/play-sessions/:sid/heartbeat`). The count has a 5-minute lag — a player who just joined won't appear until their first heartbeat lands, and a player who disconnected stays counted for up to 5 minutes.

---

## Quick Play says "No players found"

**Symptom:** A player clicks Quick Play, waits, and after 60 seconds sees "No players found. Try creating a lobby instead."

**Cause:** Not enough players were online and queued for that game within the 60-second matchmaking window. Quick Play matches from a per-game queue, so it only succeeds when other players are searching at the same time. New or niche games with small player bases will time out frequently.

**Solution:** This isn't a bug — there simply weren't enough opponents. Have the player use **Create Lobby** and share the code with friends instead. Quick Play works best for popular games with active player bases; for a small audience, direct lobby codes are the reliable path.

---

## Messages arrive out of order

**Symptom:** Game state updates from a peer arrive in the wrong sequence.

**Cause:** WebRTC data channels are created with `ordered: true`, so messages on a single open channel arrive in order. However, when a channel fails and the shim reconnects (or falls back to relay), a **new** channel is created. Messages sent on the old channel before failure may arrive after messages on the new channel, or be lost entirely.

**Solution:** Include a sequence number or timestamp in every game message and reorder/discard on receipt. Treat reconnection as a state-sync boundary — when `onTransportChange` fires for a peer, request a full state snapshot from that peer. The `ordered: true` guarantee only holds within a single channel's lifetime; don't rely on it across reconnects.

---

## Spectator can't send messages

**Symptom:** A spectator's `PlayMore.send()` / `sendUnreliable()` calls never reach other players; the server rejects them.

**Cause:** Expected behavior. Spectators join via `JoinSpectator` and are read-only observers — the server returns `ErrSpectator` ("spectators cannot send game messages") for any `msg` frame they send. Spectators can still receive all relayed game messages.

**Solution:** This is by design (casting, late-join viewing, moderation). Gate your input UI on `PlayMore.isSpectator()` and render the world without enabling controls for spectators. If the player needs to participate, they must join the lobby as a regular member (before start, or via rejoin if they were a former member).

---

## Host left but lobby continued

**Symptom:** The host disconnected mid-game, but `onClosed` did **not** fire and the lobby kept running with a different host.

**Cause:** Host migration is working correctly. When the host leaves a started lobby, the server promotes the next non-spectator member (by join order) to host. `PlayMore.isHost()` updates automatically — the new host now sees `true`, everyone else `false` — via the `onPlayers` broadcast.

**Solution:** Nothing to fix; this is the intended behavior. If your game uses an authoritative host, hand off authority to the promoted host and have it re-derive state from the latest peer snapshots. The lobby only closes (firing `onClosed` with reason `host_left`) when no non-spectator member remains to take over.

---

## Can't rejoin after disconnect

**Symptom:** A player who dropped out tries to rejoin with the same lobby code and gets "lobby not found" or "game already started".

**Cause:** Former members of a started lobby can rejoin (the server tracks them in `FormerMembers`), but only if the lobby still exists. A lobby is gone if it was idle-reaped after the 2-hour TTL, if it closed because no non-spectator member was left to host, or if the code is simply wrong. A player who was **never** a member of a started lobby is rejected with `ErrLobbyStarted`.

**Solution:** Verify the lobby code is correct (6 chars, case-insensitive). If the disconnect was long ago, the lobby was likely reaped after 2 hours of inactivity — it's gone for good; start a new one. If the lobby is still active, make sure the player rejoins as the **same user** they were before (same session/account) — `FormerMembers` is keyed by user ID, so a different account won't match.

---

## Connection quality is poor

**Symptom:** Movement is janky/laggy; the relay is getting backed up; players see stale state.

**Cause:** High RTT between peers (often because WebRTC fell back to the relay, which routes through the server). Polling `PlayMore.ping(peerId)` returns the per-peer RTT; values above ~150 ms or `-1` (relay) indicate a poor path. Sending at a fixed high rate over a slow path queues frames faster than they drain, so updates arrive late or out of order.

**Solution:** Adapt to the connection. Switch high-frequency state (position, velocity) to `PlayMore.sendUnreliable()` so stale in-flight frames are dropped instead of retransmitted and queued. Throttle your send rate using `PlayMore.recommendedThrottle()` — it returns 33/66/100 ms based on average RTT, backing off automatically on poor links. Reserve the reliable `send()` for discrete events that must arrive. Watch `onPingChange` to react to degradation in real time.
