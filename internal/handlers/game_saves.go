package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

// gameSaveGate runs the shared checks for every /games/:id/saves*
// endpoint and returns (user, gameID, ok). On failure it has already
// written the error response.
//
// Checks, in order:
//  1. Authenticated (the route middleware enforces this too; the nil
//     check keeps the handler safe if it's ever mounted differently).
//  2. If authenticated via pm_gs_ token, the token's game_id must
//     match :id — a token minted for game A cannot touch game B's
//     saves (same L3 scoping as play_sessions.go).
//  3. The game exists and is published, OR the caller is its
//     developer. Unpublished games 404 for everyone else — same
//     visibility rule as MintGameSessionTokenHandler.
func gameSaveGate(c *gin.Context) (*models.User, string, bool) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return nil, "", false
	}
	gameID := c.Param("id")

	// If authenticated via pm_gs_ token, verify the token's game_id matches.
	if tok := middleware.GetGameSessionToken(c); tok != nil {
		if tok.GameID != gameID {
			c.JSON(http.StatusForbidden, gin.H{"error": "token not valid for this game"})
			return nil, "", false
		}
	}

	game, err := models.GetGameByID(gameID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return nil, "", false
	}
	// Published games: any authenticated user has saves (they're a player).
	// Unpublished games: only the developer (for testing).
	if !game.Published && game.DeveloperID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return nil, "", false
	}
	return user, gameID, true
}

// PutGameSaveHandler handles PUT /api/v1/games/:id/saves/:key.
// Accepts session auth (SPA/tools) or pm_gs_ game session tokens
// (game iframe). Body is the raw JSON value to store; upserts the
// row keyed by (user, game, key).
func PutGameSaveHandler(c *gin.Context) {
	user, gameID, ok := gameSaveGate(c)
	if !ok {
		return
	}

	key := c.Param("key")
	if !models.IsValidGameSaveKey(key) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid save key (1-64 chars of a-z A-Z 0-9 . _ -)"})
		return
	}

	// Route layer caps the body at MaxGameSaveValueBytes + headroom, so
	// a wildly oversized request fails the read; a value just over the
	// cap is caught by the explicit length check for a clean 413.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "save value too large (max 64 KiB)"})
		return
	}
	if len(body) > models.MaxGameSaveValueBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "save value too large (max 64 KiB)"})
		return
	}
	if len(body) == 0 || !json.Valid(body) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "save value must be valid JSON"})
		return
	}

	save, err := models.UpsertGameSave(user.ID, gameID, key, string(body))
	if err != nil {
		if models.IsGameSaveKeyLimitError(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "save key limit reached (max 32 keys per game) — delete a key or overwrite an existing one"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store save"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"key":        save.Key,
		"size":       len(save.Value),
		"updated_at": save.UpdatedAt,
	})
}

// GetGameSaveHandler handles GET /api/v1/games/:id/saves/:key.
// Returns the stored value verbatim; 404 if the key doesn't exist
// for this (user, game).
func GetGameSaveHandler(c *gin.Context) {
	user, gameID, ok := gameSaveGate(c)
	if !ok {
		return
	}

	key := c.Param("key")
	if !models.IsValidGameSaveKey(key) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid save key (1-64 chars of a-z A-Z 0-9 . _ -)"})
		return
	}

	save, err := models.GetGameSave(user.ID, gameID, key)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "save not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load save"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"key":        save.Key,
		"value":      json.RawMessage(save.Value),
		"updated_at": save.UpdatedAt,
	})
}

// ListGameSavesHandler handles GET /api/v1/games/:id/saves.
// Returns key/size/updated_at for every save the user holds for the
// game — no values, so a full listing stays small.
func ListGameSavesHandler(c *gin.Context) {
	user, gameID, ok := gameSaveGate(c)
	if !ok {
		return
	}

	saves, err := models.ListGameSaves(user.ID, gameID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list saves"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"saves": saves})
}

// DeleteGameSaveHandler handles DELETE /api/v1/games/:id/saves/:key.
// Idempotent — deleting a key that doesn't exist still returns 204.
func DeleteGameSaveHandler(c *gin.Context) {
	user, gameID, ok := gameSaveGate(c)
	if !ok {
		return
	}

	key := c.Param("key")
	if !models.IsValidGameSaveKey(key) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid save key (1-64 chars of a-z A-Z 0-9 . _ -)"})
		return
	}

	if err := models.DeleteGameSave(user.ID, gameID, key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete save"})
		return
	}
	c.Status(http.StatusNoContent)
}
