package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

// GetFeatured returns the curated store-hero list (pins + trending/newest fill).
func GetFeatured(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "6"))
	games, err := models.GetFeaturedGames(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load featured"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"games": games})
}

// AdminGetFeatured returns the current pinned games (admin only).
func AdminGetFeatured(c *gin.Context) {
	games, err := models.GetPinnedGames()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load pins"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"games": games})
}

// AdminSetFeatured replaces the pinned set/order (admin only).
func AdminSetFeatured(c *gin.Context) {
	var input struct {
		GameIDs []string `json:"game_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"})
		return
	}
	if len(input.GameIDs) > 12 {
		input.GameIDs = input.GameIDs[:12]
	}
	if err := models.SetFeaturedPins(input.GameIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"game_ids": input.GameIDs})
}
