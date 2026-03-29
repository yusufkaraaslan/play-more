package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type Devlog struct {
	ID        string `json:"id"`
	GameID    string `json:"game_id"`
	UserID    string `json:"user_id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	GameTitle string `json:"game_title,omitempty"`
	Username  string `json:"username,omitempty"`
}

func ListDevlogs(c *gin.Context) {
	gameID := c.Param("id")
	rows, err := storage.DB.Query(
		`SELECT d.id, d.game_id, d.user_id, d.title, d.content, d.created_at, g.title, u.username
		 FROM devlogs d JOIN games g ON d.game_id = g.id JOIN users u ON d.user_id = u.id
		 WHERE d.game_id = ? ORDER BY d.created_at DESC`, gameID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list devlogs"})
		return
	}
	defer rows.Close()

	devlogs := []Devlog{}
	for rows.Next() {
		var d Devlog
		rows.Scan(&d.ID, &d.GameID, &d.UserID, &d.Title, &d.Content, &d.CreatedAt, &d.GameTitle, &d.Username)
		devlogs = append(devlogs, d)
	}
	c.JSON(http.StatusOK, gin.H{"devlogs": devlogs})
}

type devlogInput struct {
	Title   string `json:"title" binding:"required"`
	Content string `json:"content" binding:"required"`
}

func CreateDevlog(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	gameID := c.Param("id")
	game, err := models.GetGameByID(gameID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}

	var input devlogInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id := uuid.New().String()
	_, err = storage.DB.Exec(
		`INSERT INTO devlogs (id, game_id, user_id, title, content) VALUES (?, ?, ?, ?, ?)`,
		id, gameID, user.ID, input.Title, input.Content,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create devlog"})
		return
	}

	models.LogActivity(user.ID, "devlog", gameID, input.Title)
	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "devlog created"})
}

func DeleteDevlog(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	storage.DB.Exec(`DELETE FROM devlogs WHERE id = ? AND user_id = ?`, c.Param("id"), user.ID)
	c.JSON(http.StatusOK, gin.H{"message": "devlog deleted"})
}
