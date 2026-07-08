# Examples & Patterns

Reference implementations and the recurring patterns they demonstrate. All examples use the `playmore-mp.js` client; see [getting-started.md](getting-started.md) for the basics.

## Minimal Multiplayer Game

The smallest useful multiplayer game: every player is a colored dot, and moving your pointer moves your dot on everyone else's screen. Complete HTML file you can upload as `index.html`.

```html
<!DOCTYPE html>
<html><head><meta charset="utf-8">
<style>
  body { margin:0; height:100vh; background:#0f191e; overflow:hidden; }
  canvas { position:absolute; inset:0; width:100%; height:100%; }
</style></head>
<body>
<canvas id="c"></canvas>
<script src="/playmore-mp.js"></script>
<script>
(function () {
  var cv = document.getElementById("c"), x = cv.getContext("2d");
  var peers = {}, myId = null, last = 0;

  function colorFor(id) {
    var h = 0; for (var i=0;i<id.length;i++) h=(h*31+id.charCodeAt(i))>>>0;
    return "hsl(" + (h%360) + ",70%,60%)";
  }
  function norm(e) {
    var r = cv.getBoundingClientRect();
    return { x: Math.min(1,Math.max(0,(e.clientX-r.left)/r.width)),
             y: Math.min(1,Math.max(0,(e.clientY-r.top)/r.height)) };
  }
  function draw() {
    x.clearRect(0,0,cv.width,cv.height);
    for (var id in peers) {
      var p = peers[id]; if (p.x==null) continue;
      x.fillStyle = p.color;
      x.beginPath(); x.arc(p.x*cv.width, p.y*cv.height, 6, 0, Math.PI*2); x.fill();
    }
  }
  window.addEventListener("resize", function(){ cv.width=cv.clientWidth; cv.height=cv.clientHeight; draw(); });

  cv.addEventListener("pointermove", function (e) {
    var now = Date.now(); if (now - last < 66) return; last = now;
    var p = norm(e);
    if (!peers[myId]) peers[myId] = { color: colorFor(myId) };
    peers[myId].x = p.x; peers[myId].y = p.y;
    draw();
    PlayMore.send({ t:"cur", x:p.x, y:p.y });
  });

  PlayMore.onReady(function (ctx) {
    myId = ctx.you.id; cv.width = cv.clientWidth; cv.height = cv.clientHeight;
    peers[myId] = { color: colorFor(myId), x:null, y:null };
  });
  PlayMore.onMessage(function (from, d) {
    if (!d || from === myId) return;
    if (!peers[from]) peers[from] = { color: colorFor(from) };
    if (d.t === "cur") { peers[from].x = d.x; peers[from].y = d.y; draw(); }
  });
  PlayMore.onPlayers(function (players) {
    var live = {};
    (players||[]).forEach(function (p) {
      if (p.id === myId) return; live[p.id] = true;
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

## Co-op Canvas

The seeded **Co-op Canvas** demo (`POST /api/seed` creates it) is the canonical reference implementation — a shared drawing board where every player has a live colored cursor and clicking drops a dot that syncs to everyone. It's ~120 lines of game code on top of `playmore-mp.js` and is the example the SDK header comment points to.

**What it does**

- Two stacked canvases: a `dots` layer (persistent) and a `cursors` layer (redrawn each move).
- `pointermove` sends a normalized cursor position to the lobby; peers render each other's cursor + name label on the cursors layer.
- `pointerdown` drops a dot on the shared `dots` layer — drawn locally *immediately*, then sent to peers who draw it in the sender's color.

**Key patterns**

- **Throttle sends** — a 66 ms gate on `pointermove` keeps output at ~15/s, well under the platform's 30 msgs/s/peer cap, so the relay never rate-limits a busy canvas.
- **Normalize coordinates** — positions are sent as 0–1 floats, so players on different screen sizes all see the dot in the right relative spot.
- **Draw locally, then sync** — the click handler draws the dot on the local canvas *before* calling `send()`, so the local player gets zero-latency feedback; peers catch up a moment later.
- **Stable per-player colors** — each player id is hashed to a hue, so a given player is the same color on every screen with no coordination.
- **Drop players who left** — `onPlayers` reconciles the roster: anyone no longer in the list is removed from the local `peers` map and the cursor layer is redrawn.

## MP Test Arena

The seeded **MP Test Arena** demo (`POST /api/seed`) is the end-to-end diagnostic game for the multiplayer stack — a dashboard you use to *verify* that P2P, fallback, keepalive, and relay are all behaving, not a game you ship to players.

**What it shows**

- **Per-peer transport indicator** — each peer in the sidebar shows a `WEBRTC` (green) or `RELAY` (amber) badge via `PlayMore.transport(peerId)`, refreshed every 2 s.
- **Live bandwidth stats** — per-peer and aggregate `sent`/`received` byte counts via `PlayMore.stats()`.
- **Connection state change log** — `onTransportChange(peerId, transport)` appends a color-coded line to the log whenever a peer switches between WebRTC and relay.
- **Cursor sync + click-to-dot** — the same canvas mechanics as Co-op Canvas, so you can eyeball latency visually.
- **Broadcast / unicast / ping-pong test buttons** — "Broadcast Ping" sends `{t:'bcast'}` to everyone; "Unicast to #1" sends `{t:'uni'}` to the first peer; incoming `{t:'ping'}` is answered with `{t:'pong',n}` so you can confirm two-way traffic.
- **Session token display** — the `pm_gs_` session token from `ctx.sessionToken` is surfaced in the top bar.

**What it tests**

Use it after a deploy or infra change to confirm: WebRTC data channels actually go P2P (badge is green), relay fallback engages when you throttle a connection, keepalive pings don't churn, and bandwidth numbers are sane. If every peer shows `RELAY` and stats stay at zero, signaling or ICE is broken.

## Patterns

### Send locally, sync to peers

Draw or apply the local player's action **immediately**, *then* send it to peers. The local player gets instant feedback; peers reconcile a few tens of milliseconds later. This is the single biggest perceived-latency win.

```js
area.addEventListener("pointerdown", function (ev) {
  var p = norm(ev);
  drawDot(p.x, p.y, myColor);                  // draw locally now
  PlayMore.send({ t: "dot", x: p.x, y: p.y }); // ...and tell everyone
});
```

### Throttle high-frequency sends

Pointer/pointermove and game-loop ticks can fire far faster than the platform's 30 msgs/s/peer cap. Gate them with a timestamp and skip sends that are too soon. A 66 ms floor gives ~15/s — comfortable headroom under the cap and smooth enough for cursor sync.

```js
var lastSent = 0;
board.addEventListener("pointermove", function (ev) {
  var now = Date.now();
  if (now - lastSent < 66) return;   // ~15/s, under the 30/s relay cap
  lastSent = now;
  var p = norm(ev);
  PlayMore.send({ t: "cur", x: p.x, y: p.y });
});
```

For higher-tickrate games (action, physics), batch state into periodic snapshots rather than sending every input.

### Normalize coordinates

Send positions as floats in the 0–1 range, not pixel coordinates. Players may be on different screen sizes, device pixel ratios, or canvas dimensions; a normalized value maps correctly onto everyone's canvas by multiplying back out at draw time.

```js
function norm(ev) {
  var r = board.getBoundingClientRect();
  return { x: Math.min(1, Math.max(0, (ev.clientX - r.left) / r.width)),
           y: Math.min(1, Math.max(0, (ev.clientY - r.top)  / r.height)) };
}
// send:  PlayMore.send({ t:"cur", x: p.x, y: p.y });
// draw:  x.arc(p.x * canvas.width, p.y * canvas.height, 6, 0, Math.PI*2);
```

### Stable per-player colors

Hash each player's stable `id` to a hue. Every client runs the same hash, so a given player is the same color on every screen — with zero coordination, no color-pick step, and no risk of two players picking the same color.

```js
function colorFor(id) {
  var h = 0; for (var i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return "hsl(" + (h % 360) + ", 70%, 60%)";
}
```

> Ignore your own messages in `onMessage`: the relay/WebRTC layer does **not** echo your broadcasts back to you, but defensively skipping `from === myId` protects against any future change and any custom echo.

```js
PlayMore.onMessage(function (from, d) {
  if (!d || from === myId) return;   // never trust your own echo
  /* ... */
});
```
