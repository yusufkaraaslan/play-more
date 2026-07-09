# Getting Started with PlayMore Multiplayer

This guide takes a game developer from zero to a working multiplayer game in a handful of steps. You'll include the client shim, wire up its callbacks, send messages, read the lobby context, and finish with a complete copy-paste example.

## Prerequisites

- **Your game is uploaded to PlayMore.** If you haven't uploaded yet, use the web UI or the `playmore-deploy` CLI (see [../DEVELOPER.md](../DEVELOPER.md)).
- **The multiplayer flag is checked.** When uploading or editing the game, enable **"Online multiplayer"** (API: set `multiplayer: true` on game create/update). The game page then shows a Multiplayer box with a live online-player count and lets players create/share lobby codes.
- **The game runs in the PlayMore iframe.** The SDK talks to the PlayMore page (its parent) over `window.postMessage`, so it only works when the game is launched through a PlayMore lobby. Outside a lobby the script loads safely but stays idle.

## Step 1: Include the script tag

Add the client shim to your game's HTML. It's a single dependency-free file served by every PlayMore instance at `/playmore-mp.js`.

```html
<script src="/playmore-mp.js"></script>
```

This exposes a global `window.PlayMore`. The shim is idempotent — including it more than once is a no-op.

## Step 2: Register callbacks

The SDK is callback-driven. Register handlers for the four lifecycle events. Each registration returns the `PlayMore` object so you can chain, and you can register multiple handlers per event.

```js
// Fires once when the lobby has launched and you're connected.
// ctx is your snapshot of the lobby (see Step 4).
PlayMore.onReady(function (ctx) {
  console.log("Lobby " + ctx.code + " started; I am " + ctx.you.username);
});

// Fires for every message received from a peer.
//   from = sender player id (string)
//   data = whatever JSON the sender passed to PlayMore.send()
PlayMore.onMessage(function (from, data) {
  console.log("Got", data, "from", from);
});

// Fires when membership changes mid-game (someone joined or left).
//   players = current [{ id, username, avatar_url, ready, host }]
PlayMore.onPlayers(function (players) {
  console.log(players.length + " players now in the lobby");
});

// Fires when the lobby is gone (host left / connection lost).
PlayMore.onClosed(function () {
  console.log("Lobby closed.");
});
```

> Register callbacks **before** you expect events. The shim emits `ready` as soon as the platform sends the `init` frame, which can be near-instant after the game loads.

## Step 3: Send messages

Messages are arbitrary JSON, relayed verbatim and opaque to the server. Design your own schema on top.

**Broadcast** to everyone else in the lobby — omit the second argument:

```js
PlayMore.send({ t: "move", square: "e4" });
PlayMore.send({ t: "sync", pos: { x: 0.5, y: 0.3 } });
```

**Unicast** to one player — pass their player id as the second argument:

```js
PlayMore.send({ t: "whisper", text: "psst" }, targetPlayerId);
```

The shim automatically routes each send over WebRTC when the data channel is open, and falls back to the server relay otherwise. You never choose a transport — `send()` just works. Returns `PlayMore` for chaining; calls before the lobby is ready are safely ignored.

## Step 4: Use the lobby context

The `ctx` object passed to your `onReady` handler is your authoritative snapshot of the lobby:

| Field | Type | Description |
|-------|------|-------------|
| `code` | `string` | The 6-character lobby code players shared. |
| `gameId` | `string` | The PlayMore game id this lobby belongs to. |
| `you` | `{ id, username }` | This player. `id` is the stable peer id used as `from`/`to` in messages. |
| `host` | `boolean` | `true` if this player is the lobby host. Use it to decide who drives authoritative state. Updates live on host migration. |
| `players` | `array` | Initial roster: `[{ id, username, avatar_url, ready, host }]`. Use `onPlayers` for subsequent changes. |
| `sessionToken` | `string` | Platform session token for this play session (prefixed `pm_gs_`). Store/echo it if your game records play sessions. |
| `metadata` | `object \| null` | Host-authored lobby settings (map, difficulty, mode). May be `null`; updates live as the host changes it. |
| `spectator` | `boolean` | `true` if this connection joined as a read-only spectator (can receive but not send). |

The same values are available as accessor methods so you can read them from anywhere without capturing `ctx` in a closure:

```js
PlayMore.code();         // "AB3KQ2"
PlayMore.gameId();       // "8f2c-..."
PlayMore.me();           // { id, username }
PlayMore.isHost();       // true / false
PlayMore.players();      // current roster (live; reflects onPlayers updates)
PlayMore.sessionToken(); // "pm_gs_..."
PlayMore.isActive();     // true between onReady and onClosed
PlayMore.metadata();     // host-authored settings object, or null
PlayMore.isSpectator();  // true if joined as read-only spectator
```

A common pattern: treat the host as the source of truth. Non-hosts send *input* to the host; the host broadcasts *resolved state* back.

```js
PlayMore.onReady(function (ctx) {
  if (ctx.host) { startAuthoritativeSim(); }
  else          { sendInputToHost(); }
});
```

## Complete working example

A minimal multiplayer game: each player moves a colored dot, and every other player sees it move. ~30 lines.

```html
<!DOCTYPE html>
<html><head><meta charset="utf-8">
<style>
  body { margin:0; height:100vh; background:#111; overflow:hidden; }
  canvas { position:absolute; inset:0; width:100%; height:100%; }
</style></head>
<body>
<canvas id="c"></canvas>
<script src="/playmore-mp.js"></script>
<script>
(function () {
  var cv = document.getElementById("c"), x = cv.getContext("2d");
  var peers = {}, myId = null, last = 0;

  function colorFor(id) {           // stable color per player id
    var h = 0; for (var i=0;i<id.length;i++) h=(h*31+id.charCodeAt(i))>>>0;
    return "hsl(" + (h%360) + ",70%,60%)";
  }
  function norm(e) {                // normalize pointer to 0..1
    var r = cv.getBoundingClientRect();
    return { x: Math.min(1, Math.max(0,(e.clientX-r.left)/r.width)),
             y: Math.min(1, Math.max(0,(e.clientY-r.top)/r.height)) };
  }
  function draw() {
    x.clearRect(0,0,cv.width,cv.height);
    for (var id in peers) {
      var p = peers[id]; if (p.x==null) continue;
      x.fillStyle = p.color;
      x.beginPath(); x.arc(p.x*cv.width, p.y*cv.height, 6, 0, Math.PI*2); x.fill();
    }
  }
  window.addEventListener("resize", function(){ cv.width=cv.width; draw(); });

  cv.addEventListener("pointermove", function (e) {
    var now = Date.now();
    if (now - last < 66) return;     // throttle to ~15/s, under the 30/s cap
    last = now;
    var p = norm(e);
    if (!peers[myId]) peers[myId] = { color: colorFor(myId) };
    peers[myId].x = p.x; peers[myId].y = p.y;
    draw();                         // draw locally now...
    PlayMore.send({ t:"cur", x:p.x, y:p.y }); // ...then sync to peers
  });

  PlayMore.onReady(function (ctx) {
    myId = ctx.you.id; cv.width = cv.clientWidth; cv.height = cv.clientHeight;
    peers[myId] = { color: colorFor(myId), x: null, y: null };
  });
  PlayMore.onMessage(function (from, d) {
    if (!d || from === myId) return;
    if (!peers[from]) peers[from] = { color: colorFor(from) };
    if (d.t === "cur") { peers[from].x = d.x; peers[from].y = d.y; draw(); }
  });
  PlayMore.onPlayers(function (players) {
    var live = {};
    (players || []).forEach(function (p) {
      if (p.id === myId) return;
      live[p.id] = true;
      if (!peers[p.id]) peers[p.id] = { color: colorFor(p.id) };
    });
    for (var id in peers) if (id !== myId && !live[id]) delete peers[id];
    draw();
  });
  PlayMore.onClosed(function () { peers = {}; draw(); });
})();
</script>
</body></html>
```

Upload this as your game's `index.html`, check the multiplayer flag, create a lobby, share the code, open it in a second browser, and the dots will follow each other.

## Quick Play (auto-matchmaking)

Besides manually creating a lobby and sharing a code, the game page offers a **Quick Play** button that matches a player with random opponents automatically. The flow is entirely platform-side; your game code does nothing different.

1. **The player clicks "Quick Play"** on the PlayMore game page.
2. **The server queues them** and searches for other players waiting on the same game. Queued players see a live "X/Y players found" status.
3. **When enough players are found**, a lobby is auto-created, everyone is joined, readied up, and the game **launches immediately** — no manual ready-up or host start.
4. **If no match is found within 60 seconds**, the search is cancelled and the player is offered the manual **Create Lobby** fallback.
5. **Your game just receives `onReady(ctx)` like normal** — the lobby context, players, host flag, and session token arrive exactly as they do for a hand-created lobby. There is no matchmaking code for you to write.

In short, Quick Play is a drop-in discovery path that uses the same WebSocket and the same lobby lifecycle your game already handles. It works best for games with an active player base; for niche or new games, players should fall back to Create Lobby and share the code directly.

## Next steps

- **[api-reference.md](api-reference.md)** — every method, callback, and field, with exact signatures and return types.
- **[architecture.md](architecture.md)** — how P2P is negotiated, relay fallback, keepalive/reconnect, the transport lifecycle, and the sandbox model.
- **[webrtc.md](webrtc.md)** — mesh & star topology, unreliable channel, keepalive, reconnection, host migration, rejoin, connection quality, transport() and stats().
- **[authentication.md](authentication.md)** — pm_gs_ tokens, CORS, and how the game iframe gets credentials.
- **[play-sessions.md](play-sessions.md)** — track active game sessions for the online_players metric.
- **[limits.md](limits.md)** — every cap and rate limit (lobby size, message rate, frame size, token TTL) and what happens when you exceed each.
- **[examples.md](examples.md)** — the Co-op Canvas and MP Test Arena reference games, plus the recurring patterns (throttle, normalize, local-first, stable colors).
- **[troubleshooting.md](troubleshooting.md)** — common issues and solutions.
