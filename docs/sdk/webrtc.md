# WebRTC Transport

PlayMore's multiplayer system uses a **mesh topology** over WebRTC data channels for peer-to-peer game data. When WebRTC isn't available (NAT, firewall, old browser), it transparently falls back to the server relay. The `playmore-mp.js` shim handles all of this — games just call `PlayMore.send()`.

## Mesh topology

Every peer connects directly to every other peer. For N players, each player maintains N−1 connections. This works well for small lobbies (the typical PlayMore use case). There's no central game server in the data path once channels are open.

## Star topology

For games where the host should be authoritative (competitive, real-time), call `PlayMore.setTopology('star')` **before** `onReady`:

```js
PlayMore.setTopology('star');
PlayMore.onReady(function (ctx) { /* ... */ });
```

In star mode, the host connects to every peer, but non-host peers connect **only to the host**. This yields N−1 total data channels instead of mesh's N*(N−1)/2 — far fewer connections, less bandwidth, and a single source of truth. Non-host-to-non-host traffic has no direct channel, so it falls back to the relay (routed host→host→peer, or via the server).

Star is better for competitive or real-time games where the host simulates state and broadcasts it. Mesh is better for cooperative games and small lobbies where every peer should see every other peer directly. The choice is per-game and must be made before the lobby starts; it cannot be changed mid-session.

## Signaling

SDP offers/answers and ICE candidates are exchanged through the PlayMore relay (WebSocket → postMessage to the iframe). The shim wraps this entirely — the game never sees signaling messages. From the game's perspective, `PlayMore.send()` just works.

Internally:

1. The offerer creates an SDP offer and sends it via the relay.
2. The answerer receives it, creates an answer, sends it back.
3. Both sides exchange ICE candidates through the same channel.
4. Once a data channel opens, game data flows P2P.

## Deterministic offerer selection

To avoid "glare" (both sides trying to offer simultaneously), exactly one side initiates. The rule is lexicographic ID comparison:

```
shouldOffer(myId, peerId) = myId < peerId
```

The peer with the smaller ID creates the data channel and sends the offer. The other peer receives the data channel via `ondatachannel`. This is fully deterministic — no coordination needed.

## ICE servers

| Type | Default | Configuration |
|---|---|---|
| STUN | `stun:stun.l.google.com:19302` | `--stun-servers` / `PLAYMORE_STUN_SERVERS` |
| TURN | (none) | `--turn-servers` / `PLAYMORE_TURN_SERVERS` |

ICE server config is served at `GET /rtc-config`:

```json
{ "iceServers": [{ "urls": "stun:stun.l.google.com:19302" }] }
```

The SPA fetches this and passes `iceServers` to the game iframe via the `init` postMessage. The shim uses it for all `RTCPeerConnection` instances.

TURN URLs can embed credentials:

```
turn:user:pass@turn.example.com:3478
```

The server parses these into `username` and `credential` fields automatically. TURN is optional but recommended for production — it ensures connectivity behind symmetric NATs and restrictive firewalls.

## Keepalive

Each open data channel runs a ping/pong keepalive:

| Parameter | Value |
|---|---|
| Ping interval | 15 seconds |
| Pong timeout | 5 seconds |
| Action on timeout | Mark channel failed, fall back to relay |

The keepalive uses internal `__pm_ping` / `__pm_pong` messages that are intercepted by the shim and never emitted to the game.

If a pong isn't received within 5 seconds, the channel is considered dead. The shim closes it, switches that peer to relay, and attempts reconnection.

## Reconnection

When a data channel fails, the shim attempts to re-establish the P2P connection:

| Parameter | Value |
|---|---|
| Max attempts | 3 |
| Delay between attempts | 5 seconds |

If reconnection succeeds, the peer switches back to WebRTC. After 3 failed attempts, the peer stays on relay.

## Host migration

When the host leaves a started lobby, the server promotes the next non-spectator member (by join order) to host and the lobby continues — it does **not** close. `isHost()` updates automatically: the new host sees `true`, everyone else `false`, via the next `onPlayers` broadcast.

If no non-spectator member remains to take over, the lobby closes and `onClosed` fires with reason `host_left`. Spectators are never promoted. The promoted host's `ready` flag is implicitly set to `true`.

This means authoritative-host games survive the original host dropping — the new host takes over the simulation. Design your game so host authority can transfer cleanly: the new host should re-derive state from the latest peer snapshots rather than assume continuity.

## Rejoin after disconnect

Former members of a **started** lobby can rejoin using the same lobby code. The server tracks departed members in a `FormerMembers` set; a `join` from one of them is allowed even though started lobbies otherwise block new joins. Anyone who was never in the lobby still gets `ErrLobbyStarted`.

On rejoin, the SDK sees the returning player in the `players` broadcast and auto-initiates a WebRTC connection to them (mesh: everyone reconnects; star: the host and the rejoined peer reconnect). The rejoined player's `ctx` is repopulated normally — `onReady` fires for them, and existing peers see them via `onPlayers`.

If the lobby was idle-reaped after the 2-hour TTL, the code no longer resolves and rejoin fails with `ErrLobbyNotFound` — treat that as a closed lobby.

## Staggered connections

When a lobby starts with multiple players, connections are initiated with a 200ms stagger between peers. This prevents a signaling burst when, e.g., 8 players all try to connect simultaneously, which can overwhelm the relay or trigger rate limits.

## Unreliable data channel

Each peer pair opens **two** data channels:

| Label | Ordered | maxRetransmits | Used by |
|---|---|---|---|
| `pm` | yes | (default) | `PlayMore.send()` |
| `pm-rt` | no | 0 | `PlayMore.sendUnreliable()` |

The unreliable channel (`pm-rt`) drops stale in-flight frames instead of retransmitting them. Use `sendUnreliable()` for high-frequency state where only the latest value matters — position, velocity, heading. For discrete events that must arrive ("player fired", "turn ended"), use the reliable `send()`.

If the unreliable channel is unavailable (still connecting, failed, or the peer is on relay), `sendUnreliable()` transparently falls back to the reliable channel and then to the relay, so the message still arrives — the unreliable guarantee only holds while WebRTC is up.

```js
// Stale position updates are dropped, not queued.
PlayMore.sendUnreliable({ type: 'pos', x: me.x, y: me.y, seq: ++seq });
```

## Checking transport per peer

### `PlayMore.transport(peerId)`

Returns the current transport for a peer:

```javascript
const t = PlayMore.transport(somePeerId);
// 'webrtc' — data channel is open, P2P
// 'relay'  — going through the server
```

A peer is `'webrtc'` only when the data channel exists and its state is `'open'`. Otherwise it's `'relay'`.

### `PlayMore.onTransportChange(fn)`

Called whenever a peer's transport changes (e.g., WebRTC → relay on failure, or relay → WebRTC on successful reconnection):

```javascript
PlayMore.onTransportChange(function (peerId, transport) {
  console.log('Peer', peerId, 'switched to', transport);
});
```

### `PlayMore.stats()`

Returns per-peer and aggregate bandwidth statistics:

```javascript
const s = PlayMore.stats();
// {
//   sent: 12345,        // total bytes sent (all peers)
//   received: 67890,    // total bytes received (all peers)
//   peers: {
//     'player-1': { transport: 'webrtc', ping: 42,  sent: 5000, received: 8000 },
//     'player-2': { transport: 'relay',  ping: -1,  sent: 7345, received: 59890 }
//   }
// }
```

## Connection quality

The keepalive ping/pong doubles as a latency probe. Three methods build on it:

- **`PlayMore.ping(peerId)`** — returns the RTT in ms, or `-1` for relay/unknown peers. Only meaningful over WebRTC (relay has no data channel to ping).
- **`PlayMore.onPingChange(fn)`** — fires when a peer's RTT changes by ≥20 ms (including to/from `-1` on transport switches). Use it to update a connection-quality indicator without polling.
- **`PlayMore.recommendedThrottle()`** — returns an adaptive send interval (33/66/100 ms) based on average RTT, so fast links send near-real-time while poor links back off under the 30 msg/s relay cap.

```javascript
PlayMore.onPingChange(function (peerId, rtt) {
  showPingBar(peerId, rtt < 0 ? 'red' : rtt > 150 ? 'red' : rtt > 80 ? 'yellow' : 'green');
});

var lastSent = 0;
function tick() {
  if (Date.now() - lastSent >= PlayMore.recommendedThrottle()) {
    PlayMore.sendUnreliable({ type: 'pos', x: me.x, y: me.y });
    lastSent = Date.now();
  }
  requestAnimationFrame(tick);
}
requestAnimationFrame(tick);
```

Games can scale their update rate to connection quality: when RTT climbs, send less often (and prefer the unreliable channel) to avoid backing up the relay.

## When WebRTC is used vs relay

The shim tries WebRTC first and falls back per-peer:

```javascript
PlayMore.send(data, peerId);
// Tries the data channel. If it's not open, falls back to relay.
```

For broadcasts (`send(data)` without a target), each peer is handled independently — some may be on WebRTC while others are on relay. This is transparent to the game.

| Condition | Transport |
|---|---|
| Data channel open and state = `'open'` | `webrtc` |
| Data channel closed, failed, or not yet connected | `relay` |

## Why fallback matters

Some networks block WebRTC entirely:

- Corporate firewalls that drop UDP
- Symmetric NATs without TURN
- Old browsers without WebRTC support

The relay (postMessage → WebSocket → server → WebSocket → postMessage) always works because it uses standard HTTPS/WSS. Latency is higher than P2P, but the game keeps running. The shim handles the switch automatically — the game doesn't need to know which transport is in use unless it wants to optimize.

## Example: monitor transport and adjust update rate

Games with frequent state updates (e.g., real-time physics) can tune their send rate based on transport. WebRTC P2P can handle high-frequency updates; relay traffic goes through the server, so backing off reduces load and latency.

```javascript
// Per-peer tick rates (ms between updates).
const tickRates = {};

PlayMore.onReady(function (ctx) {
  // Start conservative — assume relay until WebRTC opens.
  ctx.players.forEach(function (p) {
    if (p.id !== ctx.you.id) tickRates[p.id] = 100;
  });
});

PlayMore.onTransportChange(function (peerId, transport) {
  // WebRTC P2P can handle fast updates.
  // Relay goes through the server — back off to reduce load.
  tickRates[peerId] = (transport === 'webrtc') ? 16 : 100;
  console.log('Adjusted tick rate for', peerId, '→', tickRates[peerId], 'ms');
});

// Send game state to each peer at its optimal rate.
const lastSent = {};

function gameLoop() {
  const state = getCurrentGameState();

  PlayMore.players().forEach(function (p) {
    if (p.id === PlayMore.me().id) return;

    const rate = tickRates[p.id] || 100;
    const now = Date.now();
    if (!lastSent[p.id] || now - lastSent[p.id] >= rate) {
      PlayMore.send(state, p.id);
      lastSent[p.id] = now;
    }
  });

  requestAnimationFrame(gameLoop);
}

requestAnimationFrame(gameLoop);
```

Check bandwidth to detect if relay traffic is too heavy:

```javascript
setInterval(function () {
  const stats = PlayMore.stats();
  let relayBytes = 0;
  Object.keys(stats.peers).forEach(function (id) {
    if (stats.peers[id].transport === 'relay') {
      relayBytes += stats.peers[id].sent + stats.peers[id].received;
    }
  });

  if (relayBytes > 1000000) {
    // Over 1 MB on relay — reduce update frequency globally.
    console.warn('High relay traffic, throttling updates');
  }
}, 5000);
```
