# playmore-mp.js API Reference

`playmore-mp.js` is the PlayMore multiplayer client SDK for HTML5 games.
Include it once and it exposes a global `PlayMore` object that handles
lobby signaling, WebRTC negotiation, transparent relay fallback, and
connection health — your game only deals with callbacks and `send`.

```html
<script src="/playmore-mp.js"></script>
```

The SDK is dependency-free, ~315 lines, and works in any browser that
supports `RTCPeerConnection` and `postMessage`. It is served by the
platform at `/playmore-mp.js` and is also embedded in the binary.

## Conventions

- All event-registration methods (`onReady`, `onMessage`, `onPlayers`,
  `onClosed`, `onTransportChange`, `onPingChange`) return the `PlayMore`
  object, so calls chain.
- A method registered before the lobby context arrives is queued; a
  method registered after an event already fired will **not** be
  retroactively called for that event (register `onReady` early).
- Player IDs are stable opaque strings. The current player's ID is
  available via `PlayMore.me().id`.
- All `data` payloads are JSON-serializable values, relayed verbatim and
  opaque to the server. Design your own message schema on top.

## Table of contents

- [Event handlers](#event-handlers)
  - [`PlayMore.onReady(callback)`](#playmoreonreadycallback)
  - [`PlayMore.onMessage(callback)`](#playmoreonmessagecallback)
  - [`PlayMore.onPlayers(callback)`](#playmoreonplayerscallback)
  - [`PlayMore.onClosed(callback)`](#playmoreonclosedcallback)
  - [`PlayMore.onLobbyState(callback)`](#playmoreonlobbystatecallback)
  - [`PlayMore.onMatchmaking(callback)`](#playmoreonmatchmakingcallback)
  - [`PlayMore.onLaunch(callback)`](#playmoreonlaunchcallback)
  - [`PlayMore.onTransportChange(callback)`](#playmoreontransportchangecallback)
  - [`PlayMore.onPingChange(callback)`](#playmoreonpingchangecallback)
- [Sending data](#sending-data)
  - [`PlayMore.send(data, to?)`](#playmoresenddata-to)
  - [`PlayMore.sendUnreliable(data, to?)`](#playmoresendunreliabledata-to)
- [State queries](#state-queries)
  - [`PlayMore.players()`](#playmoreplayers)
  - [`PlayMore.me()`](#playmoreme)
  - [`PlayMore.isHost()`](#playmoreishost)
  - [`PlayMore.code()`](#playmorecode)
  - [`PlayMore.gameId()`](#playmoregameid)
  - [`PlayMore.sessionToken()`](#playmoresessiontoken)
  - [`PlayMore.isActive()`](#playmoreisactive)
  - [`PlayMore.isSpectator()`](#playmoreisspectator)
  - [`PlayMore.metadata()`](#playmoremetadata)
- [Topology and lobby metadata](#topology-and-lobby-metadata)
  - [`PlayMore.setTopology(t)`](#playmoresettopologyt)
  - [`PlayMore.setMetadata(obj)`](#playmoresetmetadataobj)
- [Lobby control (game-managed lobby UI)](#lobby-control-game-managed-lobby-ui)
  - [`PlayMore.createLobby / joinLobby / quickPlay / readyUp / startGame / leaveLobby / cancelMatchmake`](#lobby-control-game-managed-lobby-ui)
- [Transport and stats](#transport-and-stats)
  - [`PlayMore.transport(peerId)`](#playmoretransportpeerid)
  - [`PlayMore.stats()`](#playmorestats)
- [Connection quality](#connection-quality)
  - [`PlayMore.ping(peerId)`](#playmorepingpeerid)
  - [`PlayMore.recommendedThrottle()`](#playmorerecommendedthrottle)
- [Cloud Saves](#cloud-saves)
- [The `ctx` object](#the-ctx-object)

---

## Event handlers

### `PlayMore.onReady(callback)`

Register a callback fired once when the platform delivers the `init`
frame — **before any lobby exists**. This is the entry point for your
**lobby menu**: `ctx.you` and the session token are available, but
`ctx.code` is `''` and `ctx.players` is empty until the player creates,
joins, or matches into a lobby (via [`createLobby`](#playmorecreatelobbyopts) /
[`joinLobby`](#playmorejoinlobbycode) / [`quickPlay`](#playmorequickplayplayercount)).
Do not start your match loop here — start it in
[`onLaunch`](#playmoreonlaunchcallback). WebRTC mesh initiation happens
at launch, not at `onReady` (unless the game loaded into an
already-started lobby, e.g. an iframe reload mid-match).

**Signature**

```js
PlayMore.onReady(callback)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `callback` | `function(ctx)` | Yes | Called with the lobby context object once the platform has delivered the `init` frame. |

**Return value**

`PlayMore` (the SDK object, for chaining).

**Description**

Register this before calling any other method. The `ctx` argument (see
[The `ctx` object](#the-ctx-object)) is the same object the SDK keeps
internally; later state queries (`players()`, `me()`, `code()`, etc.)
read from it, but the snapshot passed to the callback is taken at
launch time — use `onPlayers` to track membership changes after launch.

**Code example**

```js
PlayMore.onReady(function (ctx) {
  console.log('Lobby', ctx.code, 'host?', ctx.host);
  console.log('Me:', ctx.you.username, '(' + ctx.you.id + ')');
  ctx.players.forEach(function (p) {
    console.log(' -', p.username, p.ready ? 'ready' : 'not ready');
  });
});
```

---

### `PlayMore.onMessage(callback)`

Register a callback fired whenever a game message arrives from another
player, whether it traveled over WebRTC or the relay. The callback
receives the sender's player ID and the payload.

**Signature**

```js
PlayMore.onMessage(callback)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `callback` | `function(from, data)` | Yes | `from` is the sender's player ID (string); `data` is the JSON value the peer passed to `send`. |

**Return value**

`PlayMore` (for chaining).

**Description**

Internal keepalive frames (`__pm_ping` / `__pm_pong`) are filtered out
and never reach your callback. `data` is JSON-parsed if the inbound
frame was valid JSON; otherwise the raw string is delivered. There is
no delivery guarantee — over the relay, a slow or disconnected peer may
miss messages. For reliability-critical state, design an
ack/resend layer in your game protocol.

**Code example**

```js
PlayMore.onMessage(function (from, data) {
  if (data.type === 'move') {
    applyMove(from, data.move);
  } else if (data.type === 'sync') {
    reconcileState(from, data.state);
  }
});
```

---

### `PlayMore.onPlayers(callback)`

Register a callback fired whenever lobby membership changes mid-game
(a player joins or leaves after launch). The callback receives the
full, current player list.

**Signature**

```js
PlayMore.onPlayers(callback)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `callback` | `function(players)` | Yes | `players` is an array of player objects (see [Player shape](#player-shape)). |

**Return value**

`PlayMore` (for chaining).

**Description**

This fires after launch, whenever the platform pushes a `players` frame.
A player who left will be absent from the new list — detect departures
by diffing against the previous list. The initial player list is
delivered via `onReady`; `onPlayers` only fires on **changes** after
that.

**Code example**

```js
var present = {};
PlayMore.onReady(function (ctx) {
  ctx.players.forEach(function (p) { present[p.id] = true; });
});
PlayMore.onPlayers(function (players) {
  var seen = {};
  players.forEach(function (p) {
    seen[p.id] = true;
    if (!present[p.id]) onPlayerJoined(p);
  });
  Object.keys(present).forEach(function (id) {
    if (!seen[id]) onPlayerLeft(id);
  });
  present = seen;
});
```

---

### `PlayMore.onClosed(callback)`

Register a callback fired when the lobby is torn down. No arguments are
passed.

**Signature**

```js
PlayMore.onClosed(callback)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `callback` | `function()` | Yes | Called with no arguments. |

**Return value**

`PlayMore` (for chaining).

**Description**

Fires when the platform sends a `closed` frame — the last non-spectator
member left (host migration found no successor), the lobby expired
(2-hour idle TTL), the developer disabled multiplayer on the game, or
the server is shutting down (`reason: "server_restarting"`). After this
fires, `isActive()` returns `false` and all WebRTC peer connections are
closed. The same WebSocket can still create or join another lobby, but
the SDK does not re-initiate on its own; the player returns to the
SPA's lobby UI.

**Code example**

```js
PlayMore.onClosed(function () {
  showGameOverScreen('The lobby was closed.');
});
```

---

### `PlayMore.onTransportChange(callback)`

Register a callback fired whenever a peer's transport switches between
WebRTC (P2P) and relay. Useful for showing connection-quality indicators
or for debugging.

**Signature**

```js
PlayMore.onTransportChange(callback)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `callback` | `function(peerId, transport)` | Yes | `peerId` is the affected player ID; `transport` is `'webrtc'` or `'relay'`. |

**Return value**

`PlayMore` (for chaining).

**Description**

Fires only on **state transitions** (the previous transport differs
from the new one), not on every state write. A peer that falls back to
relay and later reconnects over WebRTC will fire this twice: once with
`'relay'`, then again with `'webrtc'`. This callback does not fire for
the initial connection attempt — use it to react to degradation and
recovery, not to detect initial connectivity.

**Code example**

```js
PlayMore.onTransportChange(function (peerId, transport) {
  if (transport === 'relay') {
    showPingIcon(peerId, 'yellow');  // relayed — higher latency
  } else {
    showPingIcon(peerId, 'green');   // P2P — direct
  }
});
```

---

### `PlayMore.onPingChange(callback)`

Register a callback fired whenever a peer's round-trip time (RTT),
measured from the keepalive ping/pong, changes by 20 ms or more. Useful
for reacting to connection-quality degradation or recovery.

**Signature**

```js
PlayMore.onPingChange(callback)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `callback` | `function(peerId, rtt)` | Yes | `peerId` is the affected player ID; `rtt` is the new RTT in milliseconds. |

**Return value**

`PlayMore` (for chaining).

**Description**

The keepalive ping runs every 15 s on each open data channel. When a
pong returns, the SDK computes the RTT; if it differs from the previous
value by at least 20 ms, every registered `onPingChange` callback fires
with the new value. RTT is `-1` for peers on the relay (no data channel
to ping), so a peer falling back to relay will fire with `-1`. This
callback fires only on meaningful changes, not on every keepalive — use
it to adjust update rates or show a connection-quality indicator
without spamming. To read the current RTT on demand, use
[`PlayMore.ping(peerId)`](#playmorepingpeerid).

**Code example**

```js
PlayMore.onPingChange(function (peerId, rtt) {
  if (rtt < 0 || rtt > 150) {
    showPingBar(peerId, 'red');     // relayed or poor
  } else if (rtt > 80) {
    showPingBar(peerId, 'yellow');
  } else {
    showPingBar(peerId, 'green');
  }
});
```

---

## Sending data

### `PlayMore.send(data, to?)`

Send a game message to other players in the lobby. Broadcast to
everyone, or unicast to a single player.

**Signature**

```js
PlayMore.send(data)
PlayMore.send(data, to)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `data` | `any` (JSON-serializable) | Yes | The payload to send. Relayed verbatim and opaque to the server. |
| `to` | `string` | No | A player ID to send to. If omitted (or falsy), the message is broadcast to every other player in the lobby. |

**Return value**

`PlayMore` (for chaining). Returns early with no effect if the lobby is
not active (`isActive()` is `false`) or the parent origin has not yet
been established.

**Description**

For each recipient, the SDK first tries the WebRTC data channel; if the
channel is not open (still connecting, failed, or never established), it
falls back to the relay transparently. Your game code is identical
regardless of transport — you do not branch on `transport()`.

`data` is `JSON.stringify`-d before transmission. Keep payloads small
(the relay caps a whole frame at 8 KiB) and batch high-frequency state
into snapshots rather than sending one message per animation frame.

**Code example**

```js
// Broadcast a state update to everyone
PlayMore.send({ type: 'state', pos: [12, 4], hp: 100 });

// Send a private action to one player
PlayMore.send({ type: 'whisper', text: 'psst' }, somePlayerId);
```

---

### `PlayMore.sendUnreliable(data, to?)`

Send a game message over the **unreliable** data channel
(`maxRetransmits: 0`, unordered). Same calling convention as `send`, but
stale in-flight frames are dropped instead of retransmitted.

**Signature**

```js
PlayMore.sendUnreliable(data)
PlayMore.sendUnreliable(data, to)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `data` | `any` (JSON-serializable) | Yes | The payload to send. Relayed verbatim and opaque to the server. |
| `to` | `string` | No | A player ID to send to. If omitted (or falsy), the message is broadcast to every other player in the lobby. |

**Return value**

`PlayMore` (for chaining). Returns early with no effect if the lobby is
not active (`isActive()` is `false`) or the parent origin has not yet
been established.

**Description**

Each peer pair maintains two data channels: `'pm'` (ordered, reliable)
used by `send`, and `'pm-rt'` (unordered, `maxRetransmits: 0`) used by
`sendUnreliable`. Use the unreliable channel for high-frequency state
updates where only the latest value matters and stale in-flight copies
should be dropped — e.g. per-frame position or velocity sync. If the
unreliable channel is unavailable (still connecting, failed, or the peer
is on relay), `sendUnreliable` falls back to the reliable channel and
then to the relay, so the message still arrives — the unreliable
guarantee only holds while WebRTC is up. For event-style messages that
must not be lost (e.g. "player fired a weapon"), use `send` instead.

**Code example**

```js
// Per-frame position sync — stale updates are dropped, not queued.
function physicsTick() {
  if (!PlayMore.isActive()) return;
  PlayMore.sendUnreliable({ type: 'pos', x: me.x, y: me.y, seq: ++seq });
}

// A discrete event that must arrive — use the reliable channel.
PlayMore.send({ type: 'shoot', target: id });
```

---

## State queries

### `PlayMore.players()`

Return a shallow copy of the current player list.

**Signature**

```js
PlayMore.players()
```

**Parameters**

None.

**Return value**

`Array<Player>` — a new array (safe to mutate) of player objects. See
[Player shape](#player-shape).

**Description**

Reflects the latest known membership, updated by `onPlayers`. Calling
this before `onReady` fires returns an empty array.

**Code example**

```js
var opponents = PlayMore.players().filter(function (p) {
  return p.id !== PlayMore.me().id;
});
```

---

### `PlayMore.me()`

Return the current player's identity.

**Signature**

```js
PlayMore.me()
```

**Parameters**

None.

**Return value**

`{ id: string, username: string } | null` — `null` before `onReady`
fires.

**Description**

The `id` is the stable opaque string other players use as the `to`
target in `send` and the `from` value in `onMessage`.

**Code example**

```js
var me = PlayMore.me();
console.log('I am', me.username, 'id', me.id);
```

---

### `PlayMore.isHost()`

Return whether the current player is the lobby host.

**Signature**

```js
PlayMore.isHost()
```

**Parameters**

None.

**Return value**

`boolean` — `true` if this player created the lobby.

**Description**

The host is the player who created the lobby, with authority to start
the game and set metadata. If the host leaves, the next non-spectator
member (by join order) is promoted and the lobby continues — `isHost()`
updates automatically on the new host (`true`) and everyone else
(`false`) via the `onPlayers` broadcast. The lobby only closes if no
non-spectator member remains to take over. Use this to gate authority:
typical patterns make the host the source of truth for shared game
state.

**Code example**

```js
if (PlayMore.isHost()) {
  startGameSimulation();
} else {
  waitForHostSnapshot();
}
```

---

### `PlayMore.code()`

Return the lobby's 6-character code.

**Signature**

```js
PlayMore.code()
```

**Parameters**

None.

**Return value**

`string` — the lobby code (e.g. `"K7P3RM"`), or `""` before `onReady`.

**Description**

The code is generated from an ambiguity-free alphabet
(`ABCDEFGHJKLMNPQRSTUVWXYZ23456789` — no `0`/`O` or `1`/`I`) so it
survives being read aloud. It is case-insensitive on join.

**Code example**

```js
var code = PlayMore.code();
showShareLink('Lobby code: ' + code);
```

---

### `PlayMore.gameId()`

Return the ID of the game the lobby was created for.

**Signature**

```js
PlayMore.gameId()
```

**Parameters**

None.

**Return value**

`string` — the game ID, or `""` before `onReady`.

**Description**

This is the same game ID used in the platform's `#game/<id>` route. It
is useful for scoping analytics or for games that host multiple modes
and need to know which game build is running.

**Code example**

```js
var gameId = PlayMore.gameId();
reportEvent('session_start', { game: gameId, lobby: PlayMore.code() });
```

---

### `PlayMore.sessionToken()`

Return the scoped game-session token issued to this iframe.

**Signature**

```js
PlayMore.sessionToken()
```

**Parameters**

None.

**Return value**

`string` — a token prefixed `pm_gs_`, or `""` before `onReady`.

**Description**

Because the game iframe runs at an opaque origin, it cannot read the
player's session cookie. The platform mints a short-lived, scoped
`pm_gs_` token and delivers it in the `init` frame. The SDK does not
use this token itself (the SPA owns the WebSocket); it is exposed for
games that need to call PlayMore REST endpoints (e.g. posting a
devlog from inside the game) without access to the cookie. Use it as a
`Bearer` token:

```js
fetch('/api/v1/games/' + PlayMore.gameId() + '/devlogs', {
  headers: { Authorization: 'Bearer ' + PlayMore.sessionToken() },
  // ...
});
```

**Code example**

```js
var token = PlayMore.sessionToken();
// token === "pm_gs_..."
```

---

### `PlayMore.isActive()`

Return whether the lobby session is currently active.

**Signature**

```js
PlayMore.isActive()
```

**Parameters**

None.

**Return value**

`boolean` — `true` from when `onReady` fires until `onClosed` fires.

**Description**

Use this to gate `send` calls in game loops that might outlive the
lobby. `send` already no-ops when inactive, but checking `isActive()`
avoids building payloads for nothing.

**Code example**

```js
function gameTick() {
  if (!PlayMore.isActive()) return;
  PlayMore.send({ type: 'tick', state: currentState() });
}
```

---

### `PlayMore.isSpectator()`

Return whether the current player joined the lobby as a spectator.

**Signature**

```js
PlayMore.isSpectator()
```

**Parameters**

None.

**Return value**

`boolean` — `true` if this connection joined as a read-only spectator.
`false` for regular players (and before `onReady` fires).

**Description**

Spectators are read-only observers: they receive every relayed game
message, but the server rejects their `send` calls with `ErrSpectator`.
They bypass the player cap and the started check, and count toward a
separate spectator cap (16 per lobby) rather than the 8-player cap. Use
this to gate UI — e.g. show the game state but disable input for
spectators.

**Code example**

```js
if (PlayMore.isSpectator()) {
  enableFreeCamera();
  disableControls();
}
```

---

### `PlayMore.metadata()`

Return the lobby's metadata object.

**Signature**

```js
PlayMore.metadata()
```

**Parameters**

None.

**Return value**

`object | null` — the opaque JSON value the host set via
`setMetadata`, or `null` if none has been set. The same value is
available on `ctx.metadata` at `onReady` and is kept current as the
host updates it.

**Description**

Metadata is an opaque, host-authored JSON value intended for game
settings (map, difficulty, mode, custom rules). The server stores and
relays it verbatim without interpreting it. The host updates it with
[`setMetadata`](#playmoresetmetadataobj); non-host players receive the
new value through the state broadcast and can read it here.

**Code example**

```js
var meta = PlayMore.metadata();
if (meta && meta.map) loadMap(meta.map);
```

---

## Topology and lobby metadata

### `PlayMore.setTopology(t)`

Set the WebRTC topology. Determines which peers the SDK initiates
data-channel connections to.

**Signature**

```js
PlayMore.setTopology(t)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `t` | `string` | Yes | `'mesh'` (default) or `'star'`. |

**Return value**

`PlayMore` (for chaining).

**Description**

Must be called **before** the lobby starts (i.e. before `onReady`
fires); once the `init` frame arrives the SDK begins connection setup
and later calls are ignored. Two modes:

- `'mesh'` (default) — every peer connects to every other peer. N−1
  connections per player. Best for cooperative games and small lobbies.
- `'star'` — only the host connects to all peers; non-host peers connect
  only to the host. N−1 total connections instead of N*(N−1)/2, so far
  fewer data channels. Non-host-to-non-host traffic falls back to the
  relay. Better for competitive or real-time games where the host is
  authoritative.

See [webrtc.md — Star topology](webrtc.md#star-topology) for the
trade-offs in detail.

**Code example**

```js
// Authoritative host for a competitive game — fewer connections.
PlayMore.setTopology('star');
PlayMore.onReady(function (ctx) { /* ... */ });
```

---

### `PlayMore.setMetadata(obj)`

Update the lobby's metadata (host-only).

**Signature**

```js
PlayMore.setMetadata(obj)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `obj` | `any` (JSON-serializable) | Yes | The new metadata value. Relayed verbatim to all members and persisted with the lobby. |

**Return value**

`PlayMore` (for chaining). Returns early with no effect if the lobby is
not active or the parent origin has not been established.

**Description**

The host authors the lobby's metadata — an opaque JSON value for game
settings (map, difficulty, mode). The server rejects calls from
non-host members with `ErrNotHost`. On success, the new value is
broadcast to all members and everyone's `ctx.metadata` (and
`PlayMore.metadata()`) updates. Use it to change a setting mid-lobby
(e.g. the host picks a new map and all clients reload). Non-hosts read
the current value via [`metadata()`](#playmoremetadata).

**Code example**

```js
if (PlayMore.isHost()) {
  PlayMore.setMetadata({ map: 'de_dust', mode: 'ctf', maxScore: 5 });
}
```

---

## Lobby control (game-managed lobby UI)

Since the game owns its own lobby menu, these methods let it create,
join, and start lobbies programmatically. All return `PlayMore` for
chaining and are safe to call from your menu buttons; their results
arrive asynchronously via the callbacks below. Lobby-*entry* commands
(`createLobby` / `joinLobby` / `quickPlay`) are queued if the socket is
still connecting and replay when it opens, so calling them straight out
of `onReady` is safe.

| Method | Description | Result callback |
|--------|-------------|-----------------|
| `PlayMore.createLobby(opts?)` | Create a lobby; caller becomes host. `opts.public` lists it in the public browser, `opts.maxPlayers` caps the lobby at 2–8 players (default 8). | `onLobbyState` |
| `PlayMore.joinLobby(code)` | Join by 6-char code (case-insensitive, whitespace-trimmed). | `onLobbyState` (or `onError`/`error` frame if not found) |
| `PlayMore.quickPlay(playerCount?)` | Auto-match with random players (default 2, clamped 2–8). | `onMatchmaking` then `onLaunch` |
| `PlayMore.readyUp(ready)` | Toggle your ready state (non-host). | `onLobbyState` |
| `PlayMore.startGame()` | Start the match (host only, once everyone is ready). | `onLaunch` for all members |
| `PlayMore.leaveLobby()` | Leave the current lobby. | `onClosed` |
| `PlayMore.cancelMatchmake()` | Leave the Quick Play queue. | — |

### `PlayMore.onLobbyState(callback)`

Fires whenever the lobby changes before launch — created, joined,
player joined/left, ready toggled, host migrated, or metadata updated.
The callback receives the full lobby snapshot
`{ code, game_id, host_id, started, max_players, players: [...], metadata }`. Use it
to (re)draw your lobby roster and enable/disable the host's Start
button. `onPlayers` still fires alongside it for membership-only logic.

### `PlayMore.onMatchmaking(callback)`

Fires while `quickPlay` searches. The callback receives
`{ queueSize, targetCount }` — show a "X/Y players" status.

### `PlayMore.onLaunch(callback)`

Fires when the match actually starts (host pressed start, or a Quick
Play queue filled). The callback receives the launched lobby snapshot.
**This is where you begin play** — hide your menu and start your game
loop. WebRTC connections to peers are initiated at this point. Contrast
with `onReady`, which fires pre-lobby for the menu.

---

## Transport and stats

### `PlayMore.transport(peerId)`

Return the current transport for a specific peer.

**Signature**

```js
PlayMore.transport(peerId)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `peerId` | `string` | Yes | A player ID from the lobby's player list. |

**Return value**

`'webrtc'` | `'relay'` — `'relay'` is also returned for an unknown peer
ID or a peer whose connection has not yet been attempted.

**Description**

Returns `'webrtc'` only when a data channel to that peer is in the
`open` state. Any other condition (connecting, failed, never
initialized, unknown ID) returns `'relay'`, because the SDK routes
traffic through the relay whenever P2P is unavailable. To track
changes over time, use `onTransportChange`.

**Code example**

```js
PlayMore.players().forEach(function (p) {
  if (p.id === PlayMore.me().id) return;
  console.log(p.username, 'via', PlayMore.transport(p.id));
});
```

---

### `PlayMore.stats()`

Return aggregate and per-peer bandwidth statistics.

**Signature**

```js
PlayMore.stats()
```

**Parameters**

None.

**Return value**

```ts
{
  sent: number,                          // total bytes sent (all peers, all transports)
  received: number,                      // total bytes received (all peers)
  peers: {
    [peerId: string]: {
      transport: 'webrtc' | 'relay',
      ping: number,                       // RTT in ms (-1 for relay/unknown)
      sent: number,                       // bytes sent to this peer
      received: number                   // bytes received from this peer
    }
  }
}
```

**Description**

Byte counts cover game payloads only — internal keepalive pings and
pongs are excluded from `received` but the raw data-channel send size
(including the JSON-encoded ping/pong envelope) is counted in `sent`.
The counts are cumulative since `onReady` and do not reset on
transport changes or reconnections. Use this for a bandwidth HUD or to
detect a peer whose traffic has stalled.

**Code example**

```js
var s = PlayMore.stats();
console.log('Total sent:', s.sent, 'received:', s.received);
Object.keys(s.peers).forEach(function (id) {
  var p = s.peers[id];
  console.log(id, p.transport, 'ping', p.ping, 'sent', p.sent, 'recv', p.received);
});
```

---

## Connection quality

### `PlayMore.ping(peerId)`

Return the round-trip time (RTT) to a peer, in milliseconds, as
measured by the keepalive ping/pong.

**Signature**

```js
PlayMore.ping(peerId)
```

**Parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `peerId` | `string` | Yes | A player ID from the lobby's player list. |

**Return value**

`number` — RTT in milliseconds, or `-1` for a peer on the relay, a peer
whose data channel has not yet opened, or an unknown peer ID.

**Description**

The SDK sends a `__pm_ping` over the reliable data channel every 15 s;
the peer replies with `__pm_pong`, and the elapsed time is the RTT. This
is the same value surfaced by `stats().peers[id].ping` and that triggers
[`onPingChange`](#playmoreonpingchangecallback). RTT is only available
over WebRTC — relayed peers have no data channel to ping, so they
report `-1`.

**Code example**

```js
PlayMore.players().forEach(function (p) {
  if (p.id === PlayMore.me().id) return;
  console.log(p.username, 'ping', PlayMore.ping(p.id), 'ms');
});
```

---

### `PlayMore.recommendedThrottle()`

Return a recommended minimum interval (ms) between high-frequency
sends, derived from the average peer RTT.

**Signature**

```js
PlayMore.recommendedThrottle()
```

**Parameters**

None.

**Return value**

`number` — `33`, `66`, or `100` (milliseconds).

**Description**

Averages the RTT across all peers with a known ping (`>= 0`) and picks
a send interval so you don't flood the network or the 30 msg/s relay
cap:

| Average RTT | Returned interval |
|---|---|
| `< 50 ms` | `33` ms (~30/s) |
| `< 150 ms` | `66` ms (~15/s) |
| `>= 150 ms` | `100` ms (~10/s) |
| no RTT data yet | `66` ms (default) |

Use it to scale your game-loop send rate to connection quality — fast
links get near-real-time updates, while poor links back off so the
relay doesn't drop frames. Pair with `sendUnreliable` for position
sync.

**Code example**

```js
var lastSent = 0;
function gameLoop() {
  var now = Date.now();
  if (now - lastSent >= PlayMore.recommendedThrottle()) {
    PlayMore.sendUnreliable({ type: 'pos', x: me.x, y: me.y });
    lastSent = now;
  }
  requestAnimationFrame(gameLoop);
}
requestAnimationFrame(gameLoop);
```

---

## Cloud Saves

Game iframes run at an **opaque origin**, so `localStorage` and
IndexedDB are unavailable — anything you store there is gone on the
next load. Cloud Saves are the durable replacement: a per-player,
per-game key-value store on the PlayMore server. Use it for anything
that should survive a reload — vehicle designs, progress, settings.

The SDK does not wrap these endpoints; call the REST API directly with
the `pm_gs_` session token from
[`PlayMore.sessionToken()`](#playmoresessiontoken). The token is
short-lived (5 min, refreshed by the platform), so read it fresh on
every request — never cache it.

**Endpoints** (all require `Authorization: Bearer <pm_gs_ token>`; the
token's game must match the `:id` in the path):

| Method & path | Description | Rate limit |
|---------------|-------------|------------|
| `PUT /api/v1/games/:id/saves/:key` | Store a value (upsert). Body is the raw JSON value. | 60 / min |
| `GET /api/v1/games/:id/saves/:key` | Fetch a value. `{ key, value, updated_at }`; 404 if absent. | 120 / min |
| `GET /api/v1/games/:id/saves` | List your keys. `{ saves: [{ key, size, updated_at }] }` — no values. | 60 / min |
| `DELETE /api/v1/games/:id/saves/:key` | Delete a key. 204; idempotent. | 60 / min |

**Constraints**

- Keys: 1–64 chars of `a-z A-Z 0-9 . _ -` (400 otherwise).
- Values: any valid JSON, max **64 KiB** (413 if larger).
- Max **32 keys** per player per game — a new key beyond that is a
  **409**; overwriting an existing key always succeeds. Batch state
  into one document per slot rather than one key per field.
- Saves are scoped to the (player, game) pair: a player only ever sees
  their own saves, and a token minted for another game gets a 403.

**Code example**

```js
function saveKey(key) {
  return '/api/v1/games/' + PlayMore.gameId() + '/saves/' + key;
}

// Store the player's current design.
function saveDesign(design) {
  return fetch(saveKey('vehicle.main'), {
    method: 'PUT',
    headers: {
      'Authorization': 'Bearer ' + PlayMore.sessionToken(),
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(design)
  });
}

// Load it back on the next session.
function loadDesign() {
  return fetch(saveKey('vehicle.main'), {
    headers: { 'Authorization': 'Bearer ' + PlayMore.sessionToken() }
  }).then(function (r) {
    if (r.status === 404) return null;   // no save yet
    return r.json().then(function (save) { return save.value; });
  });
}
```

Writes go to the server, so debounce them — save on explicit user
action (e.g. "Save design") or at checkpoints, not every frame. The
60/min PUT rate limit returns 429 when exceeded.

One subtlety: a stored literal `null` is indistinguishable from "no
save yet" in the `loadDesign()` example above (both yield `null`). If
`null` is a meaningful value in your game, branch on the 404 status
instead of the decoded value.

---

## The `ctx` object

The context object passed to the `onReady` callback. The same object is
used internally by the state-query methods.

| Field | Type | Description |
|-------|------|-------------|
| `code` | `string` | The 6-character lobby code. Same as `PlayMore.code()`. |
| `gameId` | `string` | The game ID the lobby was created for. Same as `PlayMore.gameId()`. |
| `you` | `{ id: string, username: string }` | The current player. Same as `PlayMore.me()`. |
| `host` | `boolean` | Whether this player is the host. Same as `PlayMore.isHost()`. Updates live on host migration. |
| `players` | `Array<Player>` | The player list at launch time. Updated in place by later `onPlayers` events. |
| `sessionToken` | `string` | The `pm_gs_` game-session token. Same as `PlayMore.sessionToken()`. |
| `metadata` | `object \| null` | The host-authored lobby metadata (game settings). Same as `PlayMore.metadata()`. May be `null` if none set; updates live as the host changes it. |
| `spectator` | `boolean` | `true` if this connection joined as a read-only spectator. Same as `PlayMore.isSpectator()`. |

### Player shape

Each entry in `ctx.players` (and in the arrays passed to `onPlayers`
and returned by `players()`) has this shape:

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Stable opaque player ID. Use as the `to` target in `send`. |
| `username` | `string` | The player's display name. |
| `avatar_url` | `string` | URL to the player's avatar image. |
| `ready` | `boolean` | Whether the player was ready at launch time. |
| `host` | `boolean` | Whether this player is the lobby host. |

### Full example

```html
<script src="/playmore-mp.js"></script>
<script>
  PlayMore
    .onReady(function (ctx) {
      console.log('Joined lobby', ctx.code, 'for game', ctx.gameId);
      console.log('I am', ctx.you.username, ctx.host ? '(host)' : '(guest)');
    })
    .onPlayers(function (players) {
      console.log('Players now:', players.map(function (p) { return p.username; }));
    })
    .onMessage(function (from, data) {
      console.log('Message from', from, ':', data);
    })
    .onTransportChange(function (peerId, transport) {
      console.log('Peer', peerId, 'switched to', transport);
    })
    .onClosed(function () {
      console.log('Lobby closed.');
    });

  // Later, in your game loop:
  function broadcastState(state) {
    if (PlayMore.isActive()) {
      PlayMore.send({ type: 'state', state: state });
    }
  }
</script>
```
