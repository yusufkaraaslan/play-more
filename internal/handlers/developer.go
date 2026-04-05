package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

func GetDeveloperPage(c *gin.Context) {
	username := c.Param("username")
	user, err := models.GetUserByUsername(username)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "developer not found"})
		return
	}

	page, _ := models.GetDeveloperPage(user.ID)
	stats, _ := models.GetUserStats(user.ID)

	// Get developer's games
	games, _, _ := models.ListGames(models.GameListParams{
		DevID: user.ID,
		Limit: 50,
		Page:  1,
	})

	activity, _ := models.ListActivity(user.ID, 10)

	c.JSON(http.StatusOK, gin.H{
		"user":     user,
		"page":     page,
		"stats":    stats,
		"games":    games,
		"activity": activity,
	})
}

type devPageInput struct {
	DisplayName string                `json:"display_name"`
	BannerURL   string                `json:"banner_url"`
	ThemeColor  string                `json:"theme_color"`
	About       string                `json:"about"`
	Links       []models.DeveloperLink `json:"links"`
}

func UpdateDeveloperPage(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input devPageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if input.Links == nil {
		input.Links = []models.DeveloperLink{}
	}

	// Sanitize HTML in about field to prevent XSS
	input.About = SanitizeHTML(input.About)

	if err := models.UpsertDeveloperPage(
		user.ID, input.DisplayName, input.BannerURL, input.ThemeColor, input.About, input.Links,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update page"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "developer page updated"})
}

func GetDeveloperGames(c *gin.Context) {
	username := c.Param("username")
	user, err := models.GetUserByUsername(username)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "developer not found"})
		return
	}

	games, total, _ := models.ListGames(models.GameListParams{
		DevID: user.ID,
		Limit: 50,
		Page:  1,
	})

	c.JSON(http.StatusOK, gin.H{"games": games, "total": total})
}
