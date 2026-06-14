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
