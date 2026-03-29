package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

func GetProfile(c *gin.Context) {
	username := c.Param("username")
	user, err := models.GetUserByUsername(username)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	stats, _ := models.GetUserStats(user.ID)
	activity, _ := models.ListActivity(user.ID, 10)

	c.JSON(http.StatusOK, gin.H{
		"user":     user,
		"stats":    stats,
		"activity": activity,
	})
}

type profileInput struct {
	Username   string        `json:"username"`
	Bio        string        `json:"bio"`
	AvatarURL  string        `json:"avatar_url"`
	BannerURL  string        `json:"banner_url"`
	ThemeColor string        `json:"theme_color"`
	Links      []models.Link `json:"links"`
}

func UpdateProfile(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input profileInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if input.Username == "" {
		input.Username = user.Username
	}
	if input.Links == nil {
		input.Links = []models.Link{}
	}

	if err := user.Update(input.Username, input.Bio, input.AvatarURL, input.BannerURL, input.ThemeColor, input.Links); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "profile updated"})
}

func GetActivity(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	activity, err := models.ListActivity(user.ID, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load activity"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"activity": activity})
}

type playtimeInput struct {
	GameID  string  `json:"game_id" binding:"required"`
	Seconds float64 `json:"seconds" binding:"required"`
}

func RecordPlaytime(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input playtimeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := models.RecordPlaytime(user.ID, input.GameID, input.Seconds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record playtime"})
		return
	}

	models.LogActivity(user.ID, "played", input.GameID, "")
	c.JSON(http.StatusOK, gin.H{"message": "playtime recorded"})
}
