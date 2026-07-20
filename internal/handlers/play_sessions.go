package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// heartbeatBody is the optional JSON body for the play-session heartbeat.
// transport_stats is reported by the multiplayer SDK (playmore-mp.js)
// every 60s so the admin dashboard can show P2P vs relay ratio and RTT.
type heartbeatBody struct {
	TransportStats map[string]lobby.PeerTransportStats `json:"transport_stats,omitempty"`
}

// OpenPlaySessionHandler handles POST /api/v1/games/:id/play-sessions.
// Accepts session auth (SPA) or pm_gs_ game session tokens (game iframe).
// Opens a new play session for the authenticated user + game.
func OpenPlaySessionHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("id")

	// If authenticated via pm_gs_ token, verify the token's game_id matches.
	if tok := middleware.GetGameSessionToken(c); tok != nil {
		if tok.GameID != gameID {
			c.JSON(http.StatusForbidden, gin.H{"error": "token not valid for this game"})
			return
		}
	}

	session, err := models.OpenPlaySession(user.ID, gameID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open play session"})
		return
	}
	c.JSON(http.StatusCreated, session)
}

// HeartbeatPlaySessionHandler handles POST /api/v1/play-sessions/:sid/heartbeat.
// Updates last_heartbeat for the session. Ownership enforced in the SQL.
// Accepts an optional JSON body with transport_stats from the multiplayer
// SDK — forwarded to the Hub for the admin multiplayer stats dashboard.
func HeartbeatPlaySessionHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	sessionID := c.Param("sid")

	// If authenticated via pm_gs_ token, verify the session belongs to
	// the token's game (L3: prevent cross-game session manipulation).
	gameID := ""
	if tok := middleware.GetGameSessionToken(c); tok != nil {
		storage.DB.QueryRow(`SELECT game_id FROM play_sessions WHERE session_id = ? AND user_id = ?`, sessionID, user.ID).Scan(&gameID)
		if gameID != tok.GameID {
			c.JSON(http.StatusForbidden, gin.H{"error": "token not valid for this session"})
			return
		}
	}

	if err := models.HeartbeatPlaySession(sessionID, user.ID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found or ended"})
		return
	}

	// Forward transport stats to the Hub if present in the body.
	var body heartbeatBody
	if c.Request.ContentLength > 0 {
		if err := json.NewDecoder(c.Request.Body).Decode(&body); err == nil && len(body.TransportStats) > 0 {
			if gameID == "" {
				storage.DB.QueryRow(`SELECT game_id FROM play_sessions WHERE session_id = ? AND user_id = ?`, sessionID, user.ID).Scan(&gameID)
			}
			lobby.Default.ReportTransportStats(sessionID, user.ID, gameID, body.TransportStats)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// EndPlaySessionHandler handles POST /api/v1/play-sessions/:sid/end.
// Marks the session as ended. Idempotent.
func EndPlaySessionHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	sessionID := c.Param("sid")

	// Same game-scoping check as heartbeat (L3).
	if tok := middleware.GetGameSessionToken(c); tok != nil {
		var gameID string
		storage.DB.QueryRow(`SELECT game_id FROM play_sessions WHERE session_id = ? AND user_id = ?`, sessionID, user.ID).Scan(&gameID)
		if gameID != tok.GameID {
			c.JSON(http.StatusForbidden, gin.H{"error": "token not valid for this session"})
			return
		}
	}

	_ = models.EndPlaySession(sessionID, user.ID)
	c.JSON(http.StatusOK, gin.H{"status": "ended"})
}
