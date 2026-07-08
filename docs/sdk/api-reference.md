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
  `onClosed`, `onTransportChange`) return the `PlayMore` object, so calls
  chain.
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
  - [`PlayMore.onTransportChange(callback)`](#playmoreontransportchangecallback)
- [Sending data](#sending-data)
  - [`PlayMore.send(data, to?)`](#playmoresenddata-to)
- [State queries](#state-queries)
  - [`PlayMore.players()`](#playmoreplayers)
  - [`PlayMore.me()`](#playmoreme)
  - [`PlayMore.isHost()`](#playmoreishost)
  - [`PlayMore.code()`](#playmorecode)
  - [`PlayMore.gameId()`](#playmoregameid)
  - [`PlayMore.sessionToken()`](#playmoresessiontoken)
  - [`PlayMore.isActive()`](#playmoreisactive)
- [Transport and stats](#transport-and-stats)
  - [`PlayMore.transport(peerId)`](#playmoretransportpeerid)
  - [`PlayMore.stats()`](#playmorestats)
- [The `ctx` object](#the-ctx-object)

---

## Event handlers

### `PlayMore.onReady(callback)`

Register a callback fired exactly once when the lobby context is
delivered to the game. This is the entry point: the lobby code, the
player list, the host flag, and the session token all become available
here. WebRTC mesh initiation begins immediately after `onReady` fires
(the SDK connects to every other player with a 200 ms stagger).

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

Fires when the platform sends a `closed` frame — the host left, the
lobby expired (2-hour idle TTL), or the developer disabled multiplayer
on the game. After this fires, `isActive()` returns `false` and all
WebRTC peer connections are closed. The same WebSocket can still create
or join another lobby, but the SDK does not re-initiate on its own; the
player returns to the SPA's lobby UI.

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

The host is the player who created the lobby. Host status is fixed for
the lobby's lifetime — there is no host migration, so if the host
leaves the lobby dies and `onClosed` fires. Use this to gate
authority: typical patterns make the host the source of truth for
shared game state.

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
      sent: number,                      // bytes sent to this peer
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
  console.log(id, p.transport, 'sent', p.sent, 'recv', p.received);
});
```

---

## The `ctx` object

The context object passed to the `onReady` callback. The same object is
used internally by the state-query methods.

| Field | Type | Description |
|-------|------|-------------|
| `code` | `string` | The 6-character lobby code. Same as `PlayMore.code()`. |
| `gameId` | `string` | The game ID the lobby was created for. Same as `PlayMore.gameId()`. |
| `you` | `{ id: string, username: string }` | The current player. Same as `PlayMore.me()`. |
| `host` | `boolean` | Whether this player is the host. Same as `PlayMore.isHost()`. |
| `players` | `Array<Player>` | The player list at launch time. Updated in place by later `onPlayers` events. |
| `sessionToken` | `string` | The `pm_gs_` game-session token. Same as `PlayMore.sessionToken()`. |

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
