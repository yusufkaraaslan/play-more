# Limits & Constraints

Every cap, rate limit, and size bound in the PlayMore multiplayer system. When a limit is hit the behavior is deterministic — this page tells you exactly what happens so you can design around it.

## Lobby & connections

| Limit | Value | Source | What happens when exceeded |
|-------|-------|--------|----------------------------|
| Max players per lobby | 8 | `lobby.MaxPlayers` | Join returns `ErrLobbyFull` ("lobby is full"). The connection stays open — the player can try another lobby. Spectators don't count toward this cap (separate `MaxSpectators=16` cap). |
| Max spectators per lobby | 16 | `lobby.MaxSpectators` | `JoinSpectator` returns `ErrLobbyFull`. Spectators bypass the player cap and the started check but can't send game messages. |
| Max lobbies per server | 500 | `lobby.MaxLobbies` | Create returns `ErrTooManyLobbies` ("server is at capacity, try again later"). Existing lobbies are unaffected. |
| Max connections per user | 4 | `lobby.MaxConnsPerUser` | The 5th WebSocket upgrade is rejected with `StatusPolicyViolation` ("too many connections"). Older connections are not kicked — close one to open another. |
| Lobby code length | 6 chars | `lobby.codeLen` | Codes are generated at exactly 6 characters from an ambiguous-character-free alphabet (`ABCDEFGHJKLMNPQRSTUVWXYZ23456789`). Matching is case-insensitive; a code of any other length simply won't resolve. |
| Idle lobby lifetime | 2 hours | `lobby.IdleTTL` | A lobby with no activity for 2 hours is reaped by the background sweeper. Members receive a `closed` frame with reason `expired`. |
| Matchmaking timeout | 60s | SPA client (`mpQuickPlay`) | After 60s with no match, the player is offered the Create Lobby fallback. The server is stateless about the deadline — it only knows whether a session is currently queued. |
| Outbound queue per connection | 64 frames | `lobby.SendBuffer` | If a client isn't draining (dead network, tab backgrounded), the 65th queued frame force-disconnects the session. |

## WebSocket rate limits

| Limit | Value | Source | What happens when exceeded |
|-------|-------|--------|----------------------------|
| Max messages per second | 30 | `wsMaxMsgsPerSec` | The 31st frame in a 1-second sliding window triggers an `error` frame ("message rate limit exceeded") and the connection is **closed**. Batch your state updates. |
| Max joins per minute | 20 | `wsMaxJoinsPerMin` | The 21st `join` in a 60-second window is rejected with `error` ("too many join attempts, slow down") — the **connection stays open**. This blunts lobby-code brute-forcing without penalizing fat-fingered codes. |
| Max frame size | 8 KiB | `wsMaxFrameBytes` | The read limit is set on upgrade. An oversized frame fails `wsjson.Read` and the connection closes. Includes envelope + payload, so leave headroom. |
| WebSocket ping interval | 30s | `wsPingEvery` | The server pings every 30s. If a pong isn't received (write timeout 10s), the connection is reaped — this catches dead sockets on mobile networks and sleeping laptops. |

## Authentication tokens

| Limit | Value | Source | What happens when exceeded |
|-------|-------|--------|----------------------------|
| Game session token TTL | 5 minutes | `models.GameSessionTokenTTL` | After 5 minutes the token is rejected with "session token expired". The SPA refreshes every ~4 min, so games should never cache the raw `pm_gs_` token — call `PlayMore.sessionToken()` each time. |
| Max active session tokens per user | 20 | `models.MaxActiveGameSessionTokensPerUser` | Minting returns "game session token limit reached" (429/400). Bounds a buggy refresh loop or an attacker hammering the mint endpoint. A user may legitimately play several games at once, so the cap is generous. |
| Max SDK keys per game | 5 | `models.MaxGameAPIKeysPerGame` | Creating a 6th key returns 400 ("maximum 5 SDK keys per game"). Delete an unused key to free a slot. |
| SDK key name max length | 100 chars | `sdk_keys.go` binding | Names longer than 100 chars fail validation: 400 "name is required (max 100 chars)". Names are unique per game. |

## Play sessions

| Limit | Value | Source | What happens when exceeded |
|-------|-------|--------|----------------------------|
| Heartbeat interval | 30s | SPA client | The SPA sends a heartbeat every 30s while a game is running. If heartbeats stop, the session eventually falls out of the active window (below). |
| Active threshold | 5 min | `CountActivePlaySessionsForGame` | A session counts toward `online_players` only if its last heartbeat is within 5 minutes and it hasn't ended. After 5 min of silence the player drops from the online count. |
| Stale threshold (GC sweep) | 24h | `CleanupStalePlaySessions` | Rows whose `last_heartbeat` is older than 24 hours are deleted by the hourly GC sweep. This is a disk-reclamation bound, not a gameplay one. |
| Open rate limit | 30 / min | route | 429 on the 31st open in a minute. |
| Heartbeat rate limit | 12 / min | route | 429 on the 13th heartbeat in a minute (one every 5s max; the SPA sends every 30s). |
| End rate limit | 10 / min | route | 429 on the 11th end in a minute. |

## WebRTC transport

| Limit | Value | Source | What happens when exceeded |
|-------|-------|--------|----------------------------|
| STUN server | Google public (configurable) | `--stun-servers` | Default: `stun:stun.l.google.com:19302`. Override with a comma-separated list. Passed to the game iframe via the `init` frame's `rtc_config.iceServers`. |
| TURN server | Optional (configurable) | `--turn-servers` | Default: none. Without a TURN server, peers behind symmetric NAT or restrictive firewalls cannot relay via TURN — they fall back to the server relay. Set this for production. |
| Keepalive ping interval | 15s | `KEEPALIVE_INTERVAL` | Each open data channel sends a `__pm_ping` every 15s. |
| Pong timeout | 5s | `PONG_TIMEOUT` | If no `__pm_pong` arrives within 5s, the channel is marked `failed` and traffic for that peer falls back to the server relay. |
| Reconnection attempts | 3 | `RECONNECT_MAX_ATTEMPTS` | After a channel fails, the shim retries up to 3 times. |
| Reconnection delay | 5s | `RECONNECT_DELAY` | Each reconnection attempt waits 5s before retrying. After 3 failed attempts the peer stays on relay. |
| Peer connection stagger | 200ms | `STAGGER_DELAY` | When a lobby launches, peer connections are initiated 200ms apart to avoid a signaling burst when all 8 players connect simultaneously. |

## Upload & body sizes

| Limit | Value | Source | What happens when exceeded |
|-------|-------|--------|----------------------------|
| Upload size limit | 500 MiB | `storage.MaxFileSize` | The request body is capped (upload cap = 500 MiB + multipart overhead). Oversized uploads are rejected by `http.MaxBytesReader`. Extracted ZIP entries exceeding 500 MiB are also rejected at the file level. |
| Image upload limit | 5 MiB | cover route body cap | Cover image uploads are capped at 5 MiB (+1 MiB overhead). Oversized requests fail with a body-read error. |
| JSON body limit | 1 MiB | `server.go` | Any `application/json` request body is capped at 1 MiB. Oversized JSON fails with a body-read error. |
| Chunked upload chunk size | 8 MiB | `chunkPutCap` | Each PUT chunk is capped at 8 MiB (+1 MiB headroom). Larger chunks fail the body-read. |

## HTTP rate limits

| Limit | Value | Source | What happens when exceeded |
|-------|-------|--------|----------------------------|
| Global rate limit | 600 req / 5 min per IP | `GlobalRateLimit(600, 300)` | 429 Too Many Requests. Applies to all `/api/` and `/api/v1/` routes. Per-IP, sliding window. |
| SDK token mint | 60 / hour | route | 429 on the 61st mint in an hour. |
| SDK key create | 10 / hour | route | 429 on the 11th creation in an hour. |

Per-endpoint rate limits (login, register, upload, etc.) are documented alongside their routes in `internal/server/routes.go`. The global limit is the floor — every API call counts against it regardless of the per-endpoint limit.
