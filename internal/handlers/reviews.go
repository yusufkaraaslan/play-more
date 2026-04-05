package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

func ListReviews(c *gin.Context) {
	gameID := c.Param("id")
	reviews, err := models.ListReviews(gameID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load reviews"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reviews": reviews})
}

type reviewInput struct {
	Rating int    `json:"rating" binding:"required,min=1,max=5"`
	Text   string `json:"text" binding:"required,min=1"`
}

func CreateReview(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input reviewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	gameID := c.Param("id")
	input.Text = SanitizePlain(input.Text)
	review, err := models.CreateReview(gameID, user.ID, input.Rating, input.Text)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "you already reviewed this game"})
		return
	}

	models.LogActivity(user.ID, "review", gameID, strconv.Itoa(input.Rating)+" stars")

	// Notify game developer
	game, _ := models.GetGameByID(gameID)
	if game != nil && game.DeveloperID != user.ID {
		CreateNotification(game.DeveloperID, "review", SanitizePlain(user.Username)+" reviewed your game \""+SanitizePlain(game.Title)+"\"", gameID, user.Username)
	}

	CheckAchievements(user.ID)
	c.JSON(http.StatusCreated, gin.H{"review": review})
}

func DeleteReview(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	if err := models.DeleteReview(c.Param("id"), user.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "review deleted"})
}
