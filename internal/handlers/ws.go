package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

const (
	// wsMaxFrameBytes caps a single inbound frame (envelope + game
	// payload). Lobby control frames are tiny; 8 KiB leaves generous
	// room for relayed game messages without letting one client buffer
	// megabytes server-side.
	wsMaxFrameBytes = 8 << 10
	// wsMaxMsgsPerSec is the per-connection inbound frame budget.
	// Enough for casual real-time games over the relay; a client that
	// exceeds it is closed (a well-behaved game batches its state).
	wsMaxMsgsPerSec = 30
	// wsMaxJoinsPerMin caps join attempts per connection. A legitimate
	// client joins a handful of times; this bounds lobby-code guessing
	// far below the 30 msg/s overall cap (1800/min → 20/min) without
	// disconnecting the client, so a fat-fingered code isn't fatal.
	wsMaxJoinsPerMin = 20
	wsWriteTimeout   = 10 * time.Second
	wsPingEvery      = 30 * time.Second
)

// GameLobbyWS upgrades GET /ws to a WebSocket and runs the multiplayer
// lobby session. Auth comes from the session cookie, a Bearer API key,
// or a pm_gs_ game session token (minted by the SPA and passed to the
// game iframe) via AuthOptional+AuthRequiredOrGameSession on the route.
//
// Game tokens bind every lobby operation to one game. Cross-origin upgrades
// are allowed only after validating an explicit game token; cookie/account
// authentication retains the WebSocket library's same-origin protection.
func GameLobbyWS(hub *lobby.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := middleware.GetUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}

		// Extract game session token's game ID for scoping (M5).
		gameSessionGameID := ""
		if tok := middleware.GetGameSessionToken(c); tok != nil {
			gameSessionGameID = tok.GameID
		}

		options := &websocket.AcceptOptions{}
		if middleware.GetGameSessionToken(c) != nil {
			// This option skips Origin validation, not TLS. Only an explicit,
			// already validated game token may bypass the cookie-origin gate.
			options.InsecureSkipVerify = true
		}
		conn, err := websocket.Accept(c.Writer, c.Request, options)
		if err != nil {
			// Accept has already written the HTTP error (including 403
			// on cross-origin handshakes).
			return
		}
		defer conn.Close(websocket.StatusInternalError, "server error")
		conn.SetReadLimit(wsMaxFrameBytes)

		sess, err := hub.Register(user.ID, user.Username, user.AvatarURL, gameSessionGameID)
		if err != nil {
			conn.Close(websocket.StatusPolicyViolation, err.Error())
			return
		}
		defer hub.Unregister(sess)

		// ctx cancels when the client disconnects (request context) or
		// when the hub force-closes the session (slow consumer, abuse).
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		credential := &wsCredential{db: storage.DB, token: middleware.GetGameSessionToken(c)}
		if credential.token != nil {
			go func() {
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if !credential.valid(ctx) {
							conn.Close(websocket.StatusPolicyViolation, "credential expired or revoked")
							sess.Close()
							return
						}
					case <-ctx.Done():
						return
					}
				}
			}()
		}
		go func() {
			select {
			case <-sess.Done():
				cancel()
			case <-ctx.Done():
			}
		}()

		// Writer: drains the hub's outbound queue onto the socket.
		go func() {
			for {
				select {
				case msg := <-sess.Out():
					wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
					err := wsjson.Write(wctx, conn, msg)
					wcancel()
					if err != nil {
						sess.Close()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		// Pinger: reaps dead connections that never send a FIN (mobile
		// networks, sleeping laptops). Pong handling needs the read
		// loop below to be running, which it is.
		go func() {
			t := time.NewTicker(wsPingEvery)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					pctx, pcancel := context.WithTimeout(ctx, wsWriteTimeout)
					err := conn.Ping(pctx)
					pcancel()
					if err != nil {
						sess.Close()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		// Reader: the handler goroutine. Exits on disconnect, malformed
		// JSON, oversized frame, or rate-limit breach. Two sliding
		// windows: an overall per-second frame budget (breach → close)
		// and a tighter per-minute join budget (breach → reject the one
		// join, keep the connection) to blunt lobby-code brute-forcing.
		var winStart, joinWinStart time.Time
		var winCount, joinCount int
		for {
			var msg lobby.ClientMsg
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				break
			}
			if now := time.Now(); now.Sub(winStart) >= time.Second {
				winStart, winCount = now, 0
			}
			if winCount++; winCount > wsMaxMsgsPerSec {
				sess.Send(lobby.ServerMsg{Type: "error", Error: "message rate limit exceeded"})
				break
			}
			if !credential.valid(ctx) {
				conn.Close(websocket.StatusPolicyViolation, "credential expired or revoked")
				return
			}
			if msg.Type == "auth" {
				if gameSessionGameID == "" {
					conn.Close(websocket.StatusPolicyViolation, "runtime token required")
					return
				}
				tok, _, err := models.ValidateGameSessionToken(msg.Token)
				if err != nil || tok.UserID != user.ID || tok.GameID != gameSessionGameID || !tok.HasRuntimeScope() {
					conn.Close(websocket.StatusPolicyViolation, "invalid replacement credential")
					return
				}
				credential.mu.Lock()
				credential.token = tok
				credential.mu.Unlock()
				sess.Send(lobby.ServerMsg{Type: "authenticated"})
				continue
			}
			if msg.Type == "join" {
				if now := time.Now(); now.Sub(joinWinStart) >= time.Minute {
					joinWinStart, joinCount = now, 0
				}
				if joinCount++; joinCount > wsMaxJoinsPerMin {
					sess.Send(lobby.ServerMsg{Type: "error", Error: "too many join attempts, slow down"})
					continue
				}
			}
			dispatchLobbyMsg(hub, sess, user, msg, gameSessionGameID)
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}
}

// dispatchLobbyMsg routes one client frame to the hub. Hub errors come
// back to the sender as an "error" frame; they never end the session.
func dispatchLobbyMsg(hub *lobby.Hub, sess *lobby.Session, user *models.User, msg lobby.ClientMsg, gameSessionGameID string) {
	var err error
	switch msg.Type {
	case "create":
		// M5: If authenticated via pm_gs_ token, scope to the token's game.
		if gameSessionGameID != "" && msg.GameID != gameSessionGameID {
			sess.Send(lobby.ServerMsg{Type: "error", Error: "token not valid for this game"})
			return
		}
		err = createLobby(hub, sess, user, msg.GameID, msg.Public, msg.MaxPlayers)
	case "join":
		if msg.Spectator {
			err = hub.JoinSpectator(sess, msg.Code)
		} else {
			err = hub.Join(sess, msg.Code)
		}
	case "leave":
		hub.Leave(sess)
	case "ready":
		err = hub.Ready(sess, msg.Ready)
	case "start":
		err = hub.Start(sess)
	case "msg":
		err = hub.Relay(sess, msg.To, msg.Data)
	case "set_metadata":
		err = hub.SetMetadata(sess, msg.Metadata)
	case "set_slots":
		err = hub.SetSlots(sess, msg.Slots)
	case "claim_slot":
		err = hub.ClaimSlot(sess, msg.SlotID)
	case "matchmake":
		// M5: scope to the pm_gs_ token's game, then apply the same
		// existence/published/multiplayer checks as "create" — Matchmake
		// itself is DB-free and would otherwise create+start a lobby for
		// any game_id an untrusted client sends.
		if gameSessionGameID != "" && msg.GameID != gameSessionGameID {
			sess.Send(lobby.ServerMsg{Type: "error", Error: "token not valid for this game"})
			return
		}
		if _, verr := validateLobbyGame(user, msg.GameID); verr != nil {
			sess.Send(lobby.ServerMsg{Type: "error", Error: verr.Error()})
			return
		}
		hub.Matchmake(sess, msg.GameID, msg.PlayerCount)
	case "cancel_matchmake":
		hub.CancelMatchmake(sess)
	default:
		sess.Send(lobby.ServerMsg{Type: "error", Error: "unknown message type"})
		return
	}
	if err != nil {
		sess.Send(lobby.ServerMsg{Type: "error", Error: err.Error()})
	}
}

// validateLobbyGame checks a game is eligible for a lobby — the hub itself
// stays DB-free. Unpublished games are visible only to their developer
// (matching GetGame), and the developer must have opted the game into
// multiplayer. Shared by the "create" and "matchmake" paths.
func validateLobbyGame(user *models.User, gameID string) (*models.Game, error) {
	game, err := models.GetGameByID(gameID)
	if err != nil {
		return nil, errGameNotFound
	}
	if !game.Published && game.DeveloperID != user.ID {
		return nil, errGameNotFound
	}
	if !game.Multiplayer {
		return nil, errNotMultiplayer
	}
	return game, nil
}

// createLobby validates the game, then opens a lobby for it.
func createLobby(hub *lobby.Hub, sess *lobby.Session, user *models.User, gameID string, public bool, maxPlayers int) error {
	game, err := validateLobbyGame(user, gameID)
	if err != nil {
		return err
	}
	if err := hub.Create(sess, game.ID, maxPlayers); err != nil {
		return err
	}
	if public {
		hub.SetPublic(sess, true)
	}
	return nil
}

var (
	errGameNotFound   = gameLobbyError("game not found")
	errNotMultiplayer = gameLobbyError("game does not support multiplayer lobbies")
)

type gameLobbyError string

func (e gameLobbyError) Error() string { return string(e) }

// The read loop owns refreshes; the idle monitor shares this immutable token
// pointer under a mutex. The captured DB never changes under either goroutine.
type wsCredential struct {
	mu    sync.Mutex
	db    *sql.DB
	token *models.GameSessionToken
}

func (a *wsCredential) valid(ctx context.Context) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token == nil {
		return true
	}
	var expires, scopes string
	var revoked bool
	if err := a.db.QueryRowContext(ctx, `SELECT expires_at, revoked, scopes FROM game_session_tokens WHERE id = ?`, a.token.ID).Scan(&expires, &revoked, &scopes); err != nil {
		return false
	}
	deadline, err := time.Parse(time.RFC3339, expires)
	return err == nil && !revoked && time.Now().Before(deadline) && (&models.GameSessionToken{Scopes: scopes}).HasRuntimeScope()
}
