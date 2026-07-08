package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

// ListGameAPIKeysHandler handles GET /api/v1/games/:id/sdk-keys.
// Session auth only (game credentials denied by AuthRequired).
// The caller must be the game's developer.
func ListGameAPIKeysHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("id")

	exists, err := models.IsGameOwner(gameID, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up game"})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}

	keys, err := models.ListGameAPIKeys(gameID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list SDK keys"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// CreateGameAPIKeyHandler handles POST /api/v1/games/:id/sdk-keys.
func CreateGameAPIKeyHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("id")

	exists, err := models.IsGameOwner(gameID, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up game"})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}

	var input struct {
		Name   string `json:"name" binding:"required,max=100"`
		Scopes string `json:"scopes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required (max 100 chars)"})
		return
	}

	key, rawKey, err := models.GenerateGameAPIKey(gameID, input.Name, input.Scopes)
	if err != nil {
		if models.IsGameKeyLimitError(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "maximum 5 SDK keys per game"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create SDK key"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"key":     key,
		"raw_key": rawKey,
		"message": "Copy this key now. You won't be able to see it again.",
	})
}

// DeleteGameAPIKeyHandler handles DELETE /api/v1/games/:id/sdk-keys/:kid.
func DeleteGameAPIKeyHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("id")
	keyID := c.Param("kid")

	exists, err := models.IsGameOwner(gameID, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up game"})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}

	if err := models.DeleteGameAPIKey(keyID, gameID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "key not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "SDK key revoked"})
}
