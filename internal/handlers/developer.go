package handlers

import (
	"log"
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
	DisplayName   string                  `json:"display_name"`
	BannerURL     string                  `json:"banner_url"`
	ThemeColor    string                  `json:"theme_color"`
	ThemePreset   string                  `json:"theme_preset"`
	About         string                  `json:"about"`
	FontHeading   string                  `json:"font_heading"`
	FontBody      string                  `json:"font_body"`
	Links         []models.DeveloperLink  `json:"links"`
	FeaturedGames []string                `json:"featured_games"`
	PageLayout    []models.PageSection    `json:"page_layout"`
	CustomCSS     string                  `json:"custom_css"`
}

func UpdateDeveloperPage(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input devPageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		log.Printf("Validation error in UpdateDeveloperPage: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input. Please check all fields and try again."})
		return
	}

	if input.Links == nil {
		input.Links = []models.DeveloperLink{}
	}
	if input.FeaturedGames == nil {
		input.FeaturedGames = []string{}
	}

	// Sanitize and limit about field (markdown, max 2000 chars)
	input.About = SanitizePlain(input.About)
	if len(input.About) > 2000 {
		input.About = input.About[:2000]
	}

	if input.PageLayout == nil {
		input.PageLayout = []models.PageSection{}
	}

	// Sanitize custom CSS (strip dangerous patterns, limit length)
	if len(input.CustomCSS) > 5000 {
		input.CustomCSS = input.CustomCSS[:5000]
	}

	if err := models.UpsertDeveloperPage(
		user.ID, input.DisplayName, input.BannerURL, input.ThemeColor, input.ThemePreset,
		input.About, input.FontHeading, input.FontBody, input.CustomCSS, input.Links, input.FeaturedGames, input.PageLayout,
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
