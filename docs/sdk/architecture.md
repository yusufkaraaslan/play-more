# PlayMore Multiplayer Architecture

How PlayMore delivers online multiplayer to HTML5 games without a
game server. This document is for **game developers** integrating
`playmore-mp.js`; it explains where data flows, who owns what, and why
the system degrades gracefully on hostile networks.

## High-level data path

```
 ┌──────────┐         ┌──────────────────┐         ┌──────────────┐
 │  Player A │──┐   ┌──│   Lobby (matchmaking)  │   │   Player B    │
 │  (SPA +   │  │   │  │  create/join/ready/start│   │  (SPA +       │
 │   iframe) │  │   │  └──────────┬─────────────┘   │   iframe)     │
 └──────────┘  │   │             │                  └──────────────┘
               │   │             │ WebSocket /ws            │
               │   │             ▼                          │
               │   │     ┌──────────────┐                   │
               │   └────►│   Signaling  │◄──────────────────┘
               │         │   (relay)    │
               │         └──────┬───────┘
               │                │
               │    ┌───────────┴────────────┐
               │    │  best path wins, per peer │
               │    │                          │
               │    ▼                          ▼
               │  ┌──────────────────┐  ┌──────────────────┐
               └─►│  WebRTC (P2P)    │  │  Relay fallback  │
                  │  data channel    │  │  via server      │
                  │  server out of   │  │  server in path  │
                  │  the data path   │  │  always works    │
                  └──────────────────┘  └──────────────────┘
```

The four stages:

1. **Lobby (matchmaking)** — players create or join a lobby via the
   SPA UI, share a 6-character code, ready up, and the host starts the
   game.
2. **Signaling (relay)** — once launched, the SDK negotiates WebRTC
   connections peer-to-peer. The signaling offers, answers, and ICE
   candidates travel through the platform's WebSocket relay because
   the game iframe cannot open its own sockets.
3. **WebRTC (P2P)** — when negotiation succeeds, game data flows over
   a direct `RTCDataChannel` between the two peers. The server is out
   of the data path: lower latency, no per-frame server cost.
4. **Fallback (relay)** — if WebRTC fails (symmetric NAT, corporate
   firewall, old browser), the SDK transparently routes that peer's
   traffic through the relay. The game never sees the switch.

## The SPA-game bridge

The game runs in a sandboxed iframe; the PlayMore SPA (the parent
window) owns the WebSocket connection to `/ws`. The two communicate
exclusively via `window.postMessage`. `playmore-mp.js` is the only
thing inside the iframe that touches `postMessage` — your game code
calls the SDK's callback API and never deals with the plumbing.

```
 ┌─────────────────────────────────── parent window (SPA) ─────────┐
 │                                                                  │
 │   WebSocket /ws  ◄────►  PlayMore server (lobby hub + relay)    │
 │         │                                                        │
 │         │  postMessage({ playmore: 'init' | 'msg' | 'players' … })│
 │         ▼                                                        │
 └──────────────────────────────────────────────────────────────────┘
                              │ ▲
                 postMessage  │ │  postMessage
 ┌────────────────────────────┘ └─────────────────────────────────┐
 │                  sandboxed iframe (your game)                    │
 │                                                                  │
 │   playmore-mp.js  ◄──►  window.parent  ◄──►  PlayMore global    │
 │                                                                  │
 │   your game logic calls PlayMore.send / onMessage / …            │
 └──────────────────────────────────────────────────────────────────┘
```

**Why this split exists:**

- The iframe is sandboxed for security and origin isolation (see
  [The sandbox model](#the-sandbox-model)). It cannot open WebSockets
  to the platform directly because it has no credentials.
- The SPA is same-origin with the server, holds the session cookie,
  and can authenticate the WebSocket. It relays frames between the
  socket and the iframe.
- `playmore-mp.js` learns the parent's real origin from the first
  inbound `postMessage` and pins all outbound frames to it, so a
  re-embedding page cannot intercept game traffic.

## Transport lifecycle

For each peer, the SDK manages a state machine over the lifetime of
the lobby:

```
 init ──► connecting ──► open (P2P) ──► failed ──► relay fallback
                ▲                              │            │
                │                              │            │
                └──────────────────────────────┘            │
                      reconnect (≤3 tries, 5 s apart)       │
                                                                  │
                  if reconnect succeeds ──► open (P2P) again ◄┘
```

| State | Meaning | What the game sees |
|-------|---------|--------------------|
| `init` | Peer discovered at lobby launch; `RTCPeerConnection` not yet created. | `transport()` → `'relay'` |
| `connecting` | Offer/answer exchange in progress; ICE gathering. | `transport()` → `'relay'` |
| `open` | Data channel is open; game data flows P2P. | `transport()` → `'webrtc'` |
| `failed` | Connection dropped, or a keepalive pong timed out. | `onTransportChange` fires with `'relay'` |
| relay fallback | Traffic for this peer routes through the server. | `transport()` → `'relay'`; `send` works normally |
| reconnecting | After 5 s, a fresh `RTCPeerConnection` is attempted (up to 3 times). | No game-visible event until it succeeds or gives up |

### How failure is detected

- **Data channel close/error events** — the browser signals a broken
  channel.
- **`connectionState` transitions** to `failed`, `disconnected`, or
  `closed`.
- **Keepalive timeout** — every 15 s the SDK sends a `__pm_ping` over
  the data channel. If no `__pm_pong` returns within 5 s, the channel
  is considered dead.

On failure the SDK closes the dead `RTCPeerConnection` and data
channel, marks the peer `failed`, routes that peer's traffic through
the relay, and schedules a reconnection attempt. If reconnection
succeeds, the peer switches back to `'webrtc'` and `onTransportChange`
fires again.

### Deterministic offerer selection

In a full mesh, every pair of players must agree on who creates the
offer. The SDK uses a deterministic rule: the peer with the
lexicographically smaller ID is the offerer (`shouldOffer: myId <
peerId`). This avoids "glare" (both sides offering simultaneously)
without any server-side coordination.

### Staggered mesh formation

When a lobby of N players launches, every player initiates connections
to all N-1 others simultaneously. To avoid a signaling burst (8
players × 7 peers = 56 near-simultaneous offer/answer exchanges), the
SDK staggers each `initPeer` call by 200 ms.

## Server vs client responsibilities

| Concern | Owned by | Notes |
|---------|----------|-------|
| Lobby creation, join, ready state | Server (hub) | In-memory for live state; persisted to SQLite (async) for restart survival. Codes are 6 chars from an ambiguity-free alphabet. |
| Host authority, start gating | Server | Only the host can start; all others must be ready. |
| Authentication | Server | Session cookie, API key, or `pm_gs_` token. Origin-checked WebSocket. |
| Relay (signaling + fallback data) | Server | Opaque payload, forwarded verbatim. 8 KiB frame cap, 30 msg/s per player. |
| Lobby lifecycle, idle reaping, host migration | Server | Host leaves → next non-spectator promoted, lobby continues; idle lobbies reaped after 2 h; persisted lobbies restored on restart. |
| WebRTC negotiation | Client (SDK) | Offer/answer/ICE exchanged via the relay as signaling. |
| Data channel management | Client (SDK) | Full mesh; ordered data channels; keepalive pings. |
| Transport selection, fallback | Client (SDK) | Tries P2P first, falls back to relay per-peer. |
| Reconnection | Client (SDK) | Up to 3 attempts, 5 s apart, then stays on relay. |
| Game logic, message schema | Client (your game) | `data` is opaque to the server; you define it. |

The server never interprets game payloads. It relays them verbatim,
which means the relay works for any game protocol but also means the
server cannot do authoritative conflict resolution — that is the
game's job (typically by trusting the host).

## The sandbox model

The game iframe is served from `/play/:id/*filepath` and is treated by
the browser as an **opaque origin**: it has no access to cookies,
`localStorage`, or the DOM of the parent page. This is deliberate —
it means a game cannot steal the player's session or read other
PlayMore data.

The problem: the game still needs to authenticate to the platform
(for the multiplayer WebSocket, and optionally for REST calls like
posting a devlog from inside the game). The session cookie is
inaccessible.

The solution is the **`pm_gs_` game-session token**:

1. When the SPA launches a lobby, it mints a short-lived, scoped
   `pm_gs_` token tied to the user and the game.
2. The token is delivered to the iframe in the `init` `postMessage`
   frame (`ctx.sessionToken`).
3. The SPA uses the token to authenticate the WebSocket on the game's
   behalf (the game never touches the socket).
4. If the game needs to call a REST endpoint directly, it can use the
   token as a `Bearer` credential — scoped to the game, short-lived,
   and revocable without affecting the user's session.

The token's primary value is **scoped, short-lived auth that does not
expose the user's session cookie to the iframe**. Even if a malicious
game exfiltrated its `pm_gs_` token, the blast radius is that game's
lobby and a narrow set of endpoints, not the user's full account.

## Why transparent fallback matters

WebRTC fails in the real world. Common failure modes:

- **Symmetric NAT** (common on mobile and carrier-grade NAT) prevents
  ICE from establishing a direct or server-reflexive path.
- **Corporate / school firewalls** block UDP entirely, or block the
  STUN servers the SDK tries.
- **Old or non-standard browsers** lack complete WebRTC support.
- **TURN servers** (which would punch through most of the above) are
  not deployed by default — PlayMore ships with a public STUN server
  only.

Without a fallback, any of these means the game is unplayable online
for that player. With transparent fallback:

- The SDK always attempts P2P first (best latency, no server load).
- If P2P fails for a given peer, that peer's traffic is routed through
  the relay — which uses the already-open WebSocket and therefore works
  wherever the WebSocket works (i.e. wherever HTTP works).
- The game code is identical regardless of transport: `send` and
  `onMessage` do not branch on `transport()`. The only observable
  difference is latency.
- If the network condition is transient (NAT mapping expires, a tab is
  backgrounded and resumed), the reconnection logic may restore P2P,
  and `onTransportChange` notifies the game.

The practical result: a game built on `playmore-mp.js` works for
every player who can load the game page, not just those on
WebRTC-friendly networks. The relay is the floor; WebRTC is the
ceiling.

## Lobby persistence

Lobbies are not purely in-memory: every membership, ready, metadata,
and start/leave transition is written to the `lobbies` SQLite table
asynchronously (via a buffered persistence worker, so the hub mutex is
never held on a DB write). Persistence is best-effort — under extreme
disk contention a write can be dropped, but the next state change
re-syncs.

On server startup, `RestoreLobbies` loads every lobby whose
`last_active` is within the 2-hour idle TTL. Their member sessions are
gone (the WebSockets died with the old process), so a restored lobby
starts with no live members; all previous member IDs are seeded into
`FormerMembers` so those players can rejoin the started lobby using the
same code. The first joiner of a restored lobby becomes the new host.

The practical effect: a server restart no longer kills in-progress
lobbies. Players see a `closed` frame with reason `server_restarting`,
then rejoin with the same code and resume. State continuity is the
game's responsibility — re-derive from peer snapshots on rejoin rather
than assuming the server kept game state (it never does; payloads are
opaque).

## Host migration

When the host of a started lobby leaves, the server promotes the next
non-spectator member (by join order) to host and the lobby continues —
it does not close. The new host's `ready` flag is set to `true`, and
the next state broadcast tells every client who the new host is;
`PlayMore.isHost()` flips automatically (the SDK reads the host flag
from the players list on every `onPlayers` event).

If no non-spectator member remains to take over, the lobby closes and
members get a `closed` frame with reason `host_left`. Spectators are
never promoted — they're read-only by design.

This makes authoritative-host games resilient: if the host's
connection drops, the lobby survives and the new host can keep the
simulation going. Games should be written so host authority can
transfer cleanly — the new host re-derives state from the latest peer
snapshots rather than assuming it inherited a coherent world.

## Rejoin after disconnect

Started lobbies normally reject new joins with `ErrLobbyStarted` —
once a game is running, strangers can't drop in. The exception is
**former members**: the server tracks every user ID that leaves a
started lobby in a `FormerMembers` set, and a `join` from one of them
is allowed through. Anyone who was never in the lobby is still
rejected.

On rejoin, the server removes the user from `FormerMembers`, re-adds
them as a member, and broadcasts the new roster. The SDK sees the
returning player and auto-initiates a WebRTC connection to them (mesh:
everyone; star: the host and the rejoined peer). The rejoined player
gets a fresh `init` frame and `onReady` fires for them as if they'd
just launched.

Rejoin fails only if the lobby is gone — reaped after the 2-hour idle
TTL, or closed because no non-spectator member was left to host. In
those cases the code no longer resolves and the player gets
`ErrLobbyNotFound`; treat it as a closed lobby.

## Spectator mode

A player can join a lobby as a read-only **spectator**
(`JoinSpectator`). Spectators bypass both the started check and the
8-player cap, so they can observe a running game without disturbing
it. They count toward a separate 16-per-lobby spectator cap instead.
The client learns it's a spectator via `ctx.spectator` /
`PlayMore.isSpectator()`.

Spectators receive every relayed game message — they see the full game
state as it's broadcast — but the server rejects their
`send`/`sendUnreliable` calls with `ErrSpectator`. They're meant for
observation (casting, late-join viewing, moderation), not
participation. Games should check `isSpectator()` to render the world
without enabling input.

Because spectators aren't counted in the player cap and are never
promoted to host, they're invisible to the authority flow: a lobby
full of spectators plus one player still has that one player as host,
and if that host leaves with only spectators remaining, the lobby
closes.

## Public lobby browser

A host can mark their lobby **public** (host-only `SetPublic` toggle).
Public, non-started lobbies for a game are listed by
`GET /api/v1/games/:id/lobbies`, which returns each lobby's code,
non-spectator player count, started flag, and host name. The SPA
renders this as a joinable list so players can find open games
without sharing codes.

Once a lobby starts, it drops out of the browser (you can't join a
running game except as a former member rejoining). Spectators aren't
counted in the listed player count. Making a lobby public is optional
and off by default — private code-only lobbies are never listed.

The browser is per-game, so each game's page shows only its own open
lobbies. Rate-limited to 30 reads/minute per IP to prevent scraping.

## Matchmaking

Quick Play is server-side matchmaking layered on the existing lobby
hub — no new endpoints, no new transport. When a player clicks
"Quick Play", the SPA sends a `matchmake` frame over the same `/ws`
WebSocket used for lobbies.

- **Per-game queue.** The hub keeps a `matchQueues` map keyed by game
  ID; each entry is an ordered slice of queued sessions. The desired
  match size (`player_count`) defaults to 2 and is clamped to the lobby
  player cap. Joining the queue first leaves any existing queue or
  lobby, so retries and reconnects self-heal.
- **Queue status broadcasts.** Every membership change in a queue
  (join, cancel, or disconnect) re-broadcasts a `matchmaking` frame to
  all remaining queued players carrying `queue_size` and `target_count`,
  so the SPA can show "X/Y players found."
- **Auto-create + auto-start.** When a queue reaches `player_count`, the
  first N players are popped off. The hub creates a lobby with the first
  queued player as host, auto-readies the rest (host is implicitly
  ready), marks the lobby `Started`, persists it, and sends a `launch`
  frame to every member. Any surplus players stay queued for the next
  match. From here the lobby behaves exactly like a hand-created one —
  host migration, rejoin, relay, and WebRTC all apply unchanged.
- **Queue cleanup on disconnect.** `Unregister` (called when a
  WebSocket closes) runs `removeFromQueueLocked`, so a queued player who
  disconnects is dropped from every queue and the remaining players get
  an updated count. Cancellation is the same path, triggered by a
  `cancel_matchmake` frame (which the game sends via
  `PlayMore.cancelMatchmake()`).

There is no built-in matchmaking deadline: the server keeps a session
queued until it matches, the game cancels, or the socket closes. A game
that wants a timeout runs its own timer and calls `cancelMatchmake()`.
The server is stateless about any deadline — it only knows whether a
session is currently queued.

## Graceful shutdown

On `SIGTERM`/`SIGINT` the server calls `Hub.Shutdown()` before exiting:
it broadcasts a `closed` frame with reason `server_restarting` to
every member of every live lobby, then tears them all down. Clients see
the reason and know to reconnect rather than treat it as a hard error.

Because lobbies are persisted, a restart is recoverable: after the new
process comes up, `RestoreLobbies` reloads the still-active lobbies
(within the idle TTL) with `FormerMembers` seeded from the previous
members, so players rejoin the same code and resume. The
`server_restarting` reason is distinct from `host_left`/`expired` so
games can show "server restarting, rejoining…" rather than "lobby
closed".

This is graceful, not instantaneous — a `kill -9` skips it. For
production, deploy via `SIGTERM` and give the process a moment to
drain.
