package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

func ListAPIKeysHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	keys, err := models.ListAPIKeys(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list keys"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

func CreateAPIKeyHandler(c *gin.Context) {
	if middleware.IsAPIKeyAuth(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "API keys cannot create other API keys"})
		return
	}
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
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

	// Force scopes to "all" — only supported value for now
	input.Scopes = "all"

	key, rawKey, err := models.GenerateAPIKey(user.ID, input.Name, input.Scopes)
	if err != nil {
		if models.IsKeyLimitError(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "maximum 10 API keys per account"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create key"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"key":     key,
		"raw_key": rawKey,
		"message": "Copy this key now. You won't be able to see it again.",
	})
}

func DeleteAPIKeyHandler(c *gin.Context) {
	if middleware.IsAPIKeyAuth(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "API keys cannot revoke other API keys"})
		return
	}
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	if err := models.DeleteAPIKey(c.Param("id"), user.ID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "key not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "API key revoked"})
}
