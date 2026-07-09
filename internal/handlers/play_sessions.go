package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

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
func HeartbeatPlaySessionHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	sessionID := c.Param("sid")

	// If authenticated via pm_gs_ token, verify the session belongs to
	// the token's game (L3: prevent cross-game session manipulation).
	if tok := middleware.GetGameSessionToken(c); tok != nil {
		var gameID string
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
