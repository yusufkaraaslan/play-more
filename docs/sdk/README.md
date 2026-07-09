# PlayMore Multiplayer SDK

PlayMore's multiplayer system gives an HTML5 game working online play with **no server code of your own**. Players create a lobby on your game's page, share a code, and launch together; the platform runs the lobby (join/ready/start) and then **relays game messages between players**. On top of that relay, the `playmore-mp.js` client shim automatically negotiates **WebRTC data channels peer-to-peer** so low-latency game data flows directly between browsers, and **transparently falls back to the server relay** for any peer WebRTC can't reach (symmetric NAT, restrictive firewall, old browser). Your game code stays identical either way — you just send and receive JSON.

## 60-second quick start

**1. Include the script** (served by every PlayMore instance):

```html
<script src="/playmore-mp.js"></script>
```

**2. Register callbacks** for the four lifecycle events:

```js
PlayMore
  .onReady(function (ctx) {        // lobby launched, you're in
    console.log("lobby", ctx.code, "me", ctx.you.username);
  })
  .onMessage(function (from, data) { // a peer sent a message
    console.log("from", from, "data", data);
  })
  .onPlayers(function (players) {    // someone joined or left
    console.log("players now", players.length);
  })
  .onClosed(function () {           // lobby ended
    console.log("lobby closed");
  });
```

**3. Send a message** — broadcast to everyone, or unicast to one player:

```js
PlayMore.send({ move: "e4" });              // broadcast to the whole lobby
PlayMore.send(state, somePlayerId);         // send to one player only
```

That's it. The shim handles WebRTC negotiation, relay fallback, keepalive, reconnection, and bandwidth accounting — you write game logic.

On the game page, players can also hit **Quick Play** to be auto-matched with random opponents instead of creating a lobby and sharing a code — the platform handles the whole flow, and your game code is unchanged. See [getting-started.md](getting-started.md#quick-play-auto-matchmaking).

## What's new

Beyond the core lobby + relay + WebRTC mesh, the SDK now supports:

- **Host migration** — if the host leaves a started lobby, the next non-spectator member is promoted and the lobby continues. `isHost()` updates automatically. ([webrtc.md](webrtc.md#host-migration))
- **Rejoin after disconnect** — former members can rejoin a started lobby with the same code; WebRTC connections re-initiate automatically. ([webrtc.md](webrtc.md#rejoin-after-disconnect))
- **Lobby persistence** — lobbies are saved to SQLite and restored on server restart, so a restart is recoverable instead of fatal. ([architecture.md](architecture.md#lobby-persistence))
- **Spectator mode** — join as a read-only observer (bypasses the player cap; separate 16-spectator cap). Check with `isSpectator()`. ([api-reference.md](api-reference.md#playmoreisspectator))
- **Public lobby browser** — hosts can mark a lobby public; `GET /api/v1/games/:id/lobbies` lists open games. ([architecture.md](architecture.md#public-lobby-browser))
- **Star topology** — `PlayMore.setTopology('star')` makes the host authoritative with N−1 connections instead of a full mesh. ([webrtc.md](webrtc.md#star-topology))
- **Unreliable channel** — `PlayMore.sendUnreliable()` drops stale in-flight frames instead of retransmitting; ideal for position sync. ([api-reference.md](api-reference.md#playmoresendunreliabledata-to))
- **Connection quality** — `ping()`, `onPingChange()`, and `recommendedThrottle()` let games adapt their update rate to RTT. ([api-reference.md](api-reference.md#connection-quality))
- **Lobby metadata** — host-authored JSON for game settings (map, mode), read via `metadata()` / `ctx.metadata`, updated with `setMetadata()`. ([api-reference.md](api-reference.md#topology-and-lobby-metadata))
- **Quick Play (matchmaking)** — players click "Quick Play" on the game page to be auto-matched with randoms; the server queues them, finds opponents, auto-creates a lobby, and launches the game. No game code required — `onReady(ctx)` fires as usual. ([getting-started.md](getting-started.md#quick-play-auto-matchmaking))

## Documentation

| Doc | What's in it |
|-----|--------------|
| [getting-started.md](getting-started.md) | Step-by-step: include the script, wire callbacks, send messages, use the lobby context. Ends with a complete copy-paste multiplayer game. |
| [api-reference.md](api-reference.md) | Every method, callback, and field on the `PlayMore` global, with signatures and return values. |
| [architecture.md](architecture.md) | How P2P transport is negotiated, when relay fallback kicks in, keepalive/reconnect behavior, the sandbox model, lobby persistence, host migration, rejoin, spectator mode, the public lobby browser, graceful shutdown, and why transparent fallback matters. |
| [webrtc.md](webrtc.md) | Mesh & star topology, unreliable channel, signaling, ICE servers, keepalive, reconnection, host migration, rejoin, connection quality, transport() and stats(). |
| [authentication.md](authentication.md) | pm_gs_ session tokens, pm_gk_ SDK keys, CORS, and the iframe auth flow. |
| [play-sessions.md](play-sessions.md) | Track active game sessions: open, heartbeat, end, and the online_players metric. |
| [examples.md](examples.md) | Reference implementations: minimal synced-dot game, Co-op Canvas, MP Test Arena — plus the recurring patterns (throttle, normalize, local-first, stable colors). |
| [limits.md](limits.md) | Every cap and rate limit — lobby size, message rate, frame size, token TTL — and exactly what happens when each is exceeded. |
| [troubleshooting.md](troubleshooting.md) | Common issues: no messages, WebRTC won't connect, CORS errors, token expired, and solutions. |

## Full API reference

For the complete method-by-method reference, see **[api-reference.md](api-reference.md)**.

For every cap and rate limit (8 players, 8 KiB/frame, 30 msgs/s, 2 h idle lobby lifetime, token TTLs, and what happens when each is exceeded), see **[limits.md](limits.md)**.

For the underlying lobby/wire protocol and non-browser clients, see **[../DEVELOPER.md](../DEVELOPER.md)**.
