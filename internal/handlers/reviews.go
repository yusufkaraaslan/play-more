package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/webhook"
)

func ListReviews(c *gin.Context) {
	gameID := c.Param("id")
	// Don't leak reviews on unpublished games to non-developers.
	game, err := models.GetGameByID(gameID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if !game.Published {
		user := middleware.GetUser(c)
		if user == nil || user.ID != game.DeveloperID {
			c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
			return
		}
	}
	reviews, err := models.ListReviews(gameID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load reviews"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reviews": reviews})
}

type reviewInput struct {
	Rating int    `json:"rating" binding:"required,min=1,max=5"`
	Text   string `json:"text" binding:"required,min=1,max=5000"`
}

func CreateReview(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input reviewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"})
		return
	}

	gameID := c.Param("id")

	// Prevent developers from reviewing their own games.
	game, err := models.GetGameByID(gameID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID == user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "you cannot review your own game"})
		return
	}

	review, err := models.CreateReview(gameID, user.ID, input.Rating, input.Text)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "you already reviewed this game"})
		return
	}

	models.LogActivity(user.ID, "review", gameID, strconv.Itoa(input.Rating)+" stars")

	// Notify game developer (re-use the game we already fetched above).
	if game.DeveloperID != user.ID {
		models.CreateNotification(game.DeveloperID, "review", user.Username+" reviewed your game \""+game.Title+"\"", gameID, user.Username)
	}

	CheckAchievements(user.ID)
	webhook.Dispatch(models.WebhookEventReviewCreated, user.ID, gin.H{
		"review_id": review.ID,
		"game_id":   gameID,
		"rating":    input.Rating,
		"developer_id": game.DeveloperID,
	})
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
