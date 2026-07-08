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
| Lobby creation, join, ready state | Server (hub) | In-memory, no DB. Codes are 6 chars from an ambiguity-free alphabet. |
| Host authority, start gating | Server | Only the host can start; all others must be ready. |
| Authentication | Server | Session cookie, API key, or `pm_gs_` token. Origin-checked WebSocket. |
| Relay (signaling + fallback data) | Server | Opaque payload, forwarded verbatim. 8 KiB frame cap, 30 msg/s per player. |
| Lobby lifecycle, idle reaping | Server | Lobbies die with their host (no migration); idle lobbies reaped after 2 h. |
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
