# WebRTC Transport

PlayMore's multiplayer system uses a **mesh topology** over WebRTC data channels for peer-to-peer game data. When WebRTC isn't available (NAT, firewall, old browser), it transparently falls back to the server relay. The `playmore-mp.js` shim handles all of this — games just call `PlayMore.send()`.

## Mesh topology

Every peer connects directly to every other peer. For N players, each player maintains N−1 connections. This works well for small lobbies (the typical PlayMore use case). There's no central game server in the data path once channels are open.

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

## Staggered connections

When a lobby starts with multiple players, connections are initiated with a 200ms stagger between peers. This prevents a signaling burst when, e.g., 8 players all try to connect simultaneously, which can overwhelm the relay or trigger rate limits.

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
//     'player-1': { transport: 'webrtc', sent: 5000, received: 8000 },
//     'player-2': { transport: 'relay',  sent: 7345, received: 59890 }
//   }
// }
```

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
