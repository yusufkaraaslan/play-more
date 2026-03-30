package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type Notification struct {
	ID        int    `json:"id"`
	UserID    string `json:"user_id"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	GameID    string `json:"game_id"`
	FromUser  string `json:"from_user"`
	Read      bool   `json:"read"`
	CreatedAt string `json:"created_at"`
}

func GetNotifications(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	rows, err := storage.DB.Query(
		`SELECT id, user_id, type, message, game_id, from_user, read, created_at
		 FROM notifications WHERE user_id = ? ORDER BY created_at DESC LIMIT 30`, user.ID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load notifications"})
		return
	}
	defer rows.Close()

	notifs := []Notification{}
	for rows.Next() {
		var n Notification
		rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Message, &n.GameID, &n.FromUser, &n.Read, &n.CreatedAt)
		notifs = append(notifs, n)
	}

	var unread int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read = 0`, user.ID).Scan(&unread)

	c.JSON(http.StatusOK, gin.H{"notifications": notifs, "unread": unread})
}

func MarkNotificationsRead(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	storage.DB.Exec(`UPDATE notifications SET read = 1 WHERE user_id = ?`, user.ID)
	c.JSON(http.StatusOK, gin.H{"message": "marked all as read"})
}

// CreateNotification is a helper called from other handlers.
func CreateNotification(userID, notifType, message, gameID, fromUser string) {
	storage.DB.Exec(
		`INSERT INTO notifications (user_id, type, message, game_id, from_user) VALUES (?, ?, ?, ?, ?)`,
		userID, notifType, message, gameID, fromUser,
	)
}
