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

## Documentation

| Doc | What's in it |
|-----|--------------|
| [getting-started.md](getting-started.md) | Step-by-step: include the script, wire callbacks, send messages, use the lobby context. Ends with a complete copy-paste multiplayer game. |
| [api-reference.md](api-reference.md) | Every method, callback, and field on the `PlayMore` global, with signatures and return values. |
| [architecture.md](architecture.md) | How P2P transport is negotiated, when relay fallback kicks in, keepalive/reconnect behavior, the sandbox model, and why transparent fallback matters. |
| [webrtc.md](webrtc.md) | Mesh topology, signaling, ICE servers, keepalive, reconnection, transport() and stats(). |
| [authentication.md](authentication.md) | pm_gs_ session tokens, pm_gk_ SDK keys, CORS, and the iframe auth flow. |
| [play-sessions.md](play-sessions.md) | Track active game sessions: open, heartbeat, end, and the online_players metric. |
| [examples.md](examples.md) | Reference implementations: minimal synced-dot game, Co-op Canvas, MP Test Arena — plus the recurring patterns (throttle, normalize, local-first, stable colors). |
| [limits.md](limits.md) | Every cap and rate limit — lobby size, message rate, frame size, token TTL — and exactly what happens when each is exceeded. |
| [troubleshooting.md](troubleshooting.md) | Common issues: no messages, WebRTC won't connect, CORS errors, token expired, and solutions. |

## Full API reference

For the complete method-by-method reference, see **[api-reference.md](api-reference.md)**.

For every cap and rate limit (8 players, 8 KiB/frame, 30 msgs/s, 2 h idle lobby lifetime, token TTLs, and what happens when each is exceeded), see **[limits.md](limits.md)**.

For the underlying lobby/wire protocol and non-browser clients, see **[../DEVELOPER.md](../DEVELOPER.md)**.
