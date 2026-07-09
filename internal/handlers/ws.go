package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
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
// When the connection is authenticated with a pm_gs_ token, the game_id
// from the token is available for scoping (e.g. restricting lobby creation
// to the token's game). The current implementation still allows any
// multiplayer game — the token's primary value is scoped, short-lived
// auth that doesn't expose the user's session cookie to the iframe.
//
// CSRF note: WebSocket handshakes are GETs, so CSRFProtect never sees
// them — the equivalent protection against cross-site WebSocket
// hijacking is the Origin check inside websocket.Accept, which rejects
// any browser handshake whose Origin host differs from the request
// Host. Non-browser clients send no Origin and pass (same stance as
// CSRF-exempt API-key requests).
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

		conn, err := websocket.Accept(c.Writer, c.Request, nil)
		if err != nil {
			// Accept has already written the HTTP error (including 403
			// on cross-origin handshakes).
			return
		}
		defer conn.Close(websocket.StatusInternalError, "server error")
		conn.SetReadLimit(wsMaxFrameBytes)

		sess, err := hub.Register(user.ID, user.Username, user.AvatarURL)
		if err != nil {
			conn.Close(websocket.StatusPolicyViolation, err.Error())
			return
		}
		defer hub.Unregister(sess)

		// ctx cancels when the client disconnects (request context) or
		// when the hub force-closes the session (slow consumer, abuse).
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
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
		err = createLobby(hub, sess, user, msg.GameID, msg.Public)
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
	case "matchmake":
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

// createLobby validates the game before opening a lobby for it — the
// hub itself stays DB-free. Unpublished games are visible only to
// their developer (matching GetGame), and the developer must have
// opted the game into multiplayer.
func createLobby(hub *lobby.Hub, sess *lobby.Session, user *models.User, gameID string, public bool) error {
	game, err := models.GetGameByID(gameID)
	if err != nil {
		return errGameNotFound
	}
	if !game.Published && game.DeveloperID != user.ID {
		return errGameNotFound
	}
	if !game.Multiplayer {
		return errNotMultiplayer
	}
	if err := hub.Create(sess, game.ID); err != nil {
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
