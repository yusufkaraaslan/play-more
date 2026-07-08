package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

// MintGameSessionTokenHandler handles POST /api/v1/games/:id/sdk-token.
// Session auth only — the SPA parent mints a token for the game iframe.
//
// Any authenticated user can mint a token for a PUBLISHED game (they're
// a player). Only the game's developer can mint for an unpublished game
// (testing). This fixes the original dev-api branch's owner-only gate
// which blocked players from getting tokens for games they didn't make.
func MintGameSessionTokenHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("id")

	game, err := models.GetGameByID(gameID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}

	// Published games: any authenticated user can mint (they're a player).
	// Unpublished games: only the developer can mint (for testing).
	if !game.Published && game.DeveloperID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}

	var input struct {
		Scopes string `json:"scopes"`
	}
	_ = c.ShouldBindJSON(&input)

	tok, rawToken, err := models.MintGameSessionToken(user.ID, gameID, input.Scopes)
	if err != nil {
		if models.IsGameSessionTokenLimitError(err) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "session token limit reached (max 20 per user)"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mint session token"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"token":      rawToken,
		"token_id":   tok.ID,
		"expires_at": tok.ExpiresAt,
		"scopes":     tok.Scopes,
	})
}

// RevokeGameSessionTokenHandler handles DELETE /api/v1/sdk-tokens/:id.
// Users can only revoke their own tokens.
func RevokeGameSessionTokenHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	tokenID := c.Param("id")

	if err := models.RevokeGameSessionToken(tokenID, user.ID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "session token revoked"})
}
