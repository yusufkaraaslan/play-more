package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
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
	Username      string        `json:"username"`
	Bio           string        `json:"bio"`
	AvatarURL     string        `json:"avatar_url"`
	BannerURL     string        `json:"banner_url"`
	ThemeColor    string        `json:"theme_color"`
	Links         []models.Link `json:"links"`
	AutoplayMedia *bool         `json:"autoplay_media"`
}

func UpdateProfile(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input profileInput
	if err := c.ShouldBindJSON(&input); err != nil {
		log.Printf("Validation error in UpdateProfile: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input. Please check all fields and try again."})
		return
	}

	if input.Username == "" {
		input.Username = user.Username
	}
	if input.Username != user.Username && !IsValidUsername(input.Username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username must be 3-30 characters (letters, numbers, underscores, hyphens) and not a reserved name."})
		return
	}
	if input.Links == nil {
		input.Links = []models.Link{}
	}

	input.Bio = SanitizePlain(input.Bio)

	autoplay := user.AutoplayMedia
	if input.AutoplayMedia != nil {
		autoplay = *input.AutoplayMedia
	}

	if err := user.Update(input.Username, input.Bio, input.AvatarURL, input.BannerURL, input.ThemeColor, input.Links, autoplay); err != nil {
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
		log.Printf("Validation error in RecordPlaytime: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input. Please check all fields and try again."})
		return
	}

	// Confirm the game exists and is published — stops attackers inflating
	// play_count + popularity sort + achievements for arbitrary IDs.
	var published int
	err := storage.DB.QueryRow(`SELECT published FROM games WHERE id = ?`, input.GameID).Scan(&published)
	if err != nil || published != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}

	// Clamp per-call seconds to a sane upper bound. Frontend sends a heartbeat
	// every ~minute; 600s gives margin for slow networks but caps abuse.
	if input.Seconds < 0 {
		input.Seconds = 0
	}
	if input.Seconds > 600 {
		input.Seconds = 600
	}

	if err := models.RecordPlaytime(user.ID, input.GameID, input.Seconds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record playtime"})
		return
	}

	models.LogActivity(user.ID, "played", input.GameID, "")
	CheckAchievements(user.ID)
	c.JSON(http.StatusOK, gin.H{"message": "playtime recorded"})
}
