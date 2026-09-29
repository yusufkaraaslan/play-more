# External web and native games

The same service can serve PlayMore-hosted games, games on another website,
and native clients. Each connection has a player identity and one registered
game. Store hosting is not required by the transport, but games and players
currently still need PlayMore records. This is not yet a separately deployable
multiplayer service or an integration with arbitrary third-party identities.

## Credentials and player identity

Use a **player-specific, five-minute `pm_gs_` token** with `session:write` scope.
The existing `POST /api/v1/games/:id/sdk-token` endpoint issues one for the
caller authenticated by a PlayMore session or user API key. The game must be
published, or the caller must own it. Never mint every player's token using
the developer's account: that authenticates everyone as the same person.

An external app must arrange authenticated token delivery for each PlayMore
player. A backend acting for that player may call the mint endpoint; keep its
account credential on that backend. The JavaScript example below assumes you
have implemented `/my-game/session-token` on your own authenticated backend.
It is **not** a bundled PlayMore route. Third-party login federation and guest
identity exchange are not implemented yet.

Never ship `pm_k_`, `pm_gk_`, or a TURN shared secret in a web bundle or native
executable. Do not accept a client-supplied player ID as proof of identity.
Game SDK keys (`pm_gk_`) cannot connect to `/ws` or impersonate a player.

## External browser SDK

Load `/playmore-mp.js` from your PlayMore service, then connect explicitly:

```javascript
PlayMore.onLobbyState(renderLobby);
PlayMore.onMessage((from, data) => applyGameMessage(from, data));
PlayMore.onError(message => showError(message));
PlayMore.onClosed(() => showReconnectButton());

await PlayMore.connect({
  server: 'https://playmore.example.com',
  gameId: 'YOUR_REGISTERED_GAME_ID',
  getToken: async () => {
    const response = await fetch('/my-game/session-token', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: '{}'
    });
    if (!response.ok) throw new Error('Sign in to play');
    return (await response.json()).token;
  }
});
PlayMore.createLobby({ public: true, maxPlayers: 4 });
// Later: PlayMore.joinLobby(code), readyUp(true), startGame(), send(data).
// When leaving the game: PlayMore.disconnect().
```

The callback is called again every four minutes. It must obtain a fresh token
for the **same player and game**. The SDK fetches fresh ICE credentials, then
reauthenticates the existing socket without leaving its lobby. Failed refreshes
retry after 30 seconds. If the connection closes, call `connect` again and
rejoin using your saved lobby code; socket reconnection is explicit. Peer WebRTC
reconnection continues to work as before. `disconnect` cancels pending startup
and refresh work and clears the runtime token.

Embedded PlayMore games continue using the existing parent-page bridge without
calling `connect`. HTTPS is required for direct mode except on localhost.

## Native protocol

Native clients can use an ordinary secure WebSocket client:

1. Fetch `GET /rtc-config` with `Authorization: Bearer pm_gs_...` to obtain
   `iceServers`, `game_id`, and `you` (player ID and username).
2. Connect to `wss://playmore.example.com/ws` with the same Authorization header.
   Browser clients use `?token=pm_gs_...` because their WebSocket API cannot set
   that header. Configure any reverse proxy to redact query strings as well.
3. Exchange the JSON messages below. Read lobby snapshots to obtain player IDs
   and the lobby code. See [the protocol types](../../internal/lobby/protocol.go).
4. Before expiry, obtain a replacement token and send `auth`. The server
   responds with `authenticated`. A different player or game is rejected.

```json
{"type":"create","game_id":"YOUR_REGISTERED_GAME_ID","max_players":4}
{"type":"join","code":"ABCDEF"}
{"type":"ready","ready":true}
{"type":"start"}
{"type":"msg","data":{"move":"e4"}}
{"type":"auth","token":"pm_gs_REPLACEMENT"}
```

Native clients can start with WebSocket relay transport. Native WebRTC requires
an engine/platform WebRTC implementation and compatibility with the JavaScript
SDK's signaling messages; a native P2P SDK is not supplied. Relay frames are
limited to 8 KiB and 30 inbound messages/second per connection. Browser/native
cross-play over relay also requires the same game-message schema.

## Security boundaries and deployment

- Valid runtime tokens permit cross-origin WebSocket connections; cookie-based
  connections retain same-origin checks. CORS and Origin are not player identity.
- Tokens restrict create, matchmaking, join, and spectate operations to one game.
- Every incoming command checks expiry, revocation, and runtime scope. Idle
  connections are checked once per second and closed when invalid.
- Expiring a credential closes its service connection; it cannot forcibly stop
  malicious clients already communicating directly over P2P.
- TURN provides connectivity, not game-rule validation. The relay also forwards
  opaque game messages. Competitive games need authoritative rule validation.
- The lobby hub and rate limits are process-local. Do not add replicas and expect
  shared matchmaking. Separate deployment/storage and external identity adapters
  are the remaining architectural work for extracting a multiplayer service.
