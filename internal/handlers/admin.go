package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

func adminLog(c *gin.Context, action string) {
	user := middleware.GetUser(c)
	if user != nil {
		log.Printf("[ADMIN] user=%s action=%s ip=%s", user.Username, action, c.ClientIP())
	}
}

func isAdmin(c *gin.Context) bool {
	user := middleware.GetUser(c)
	if user == nil {
		return false
	}
	// First registered user is admin
	var firstID string
	storage.DB.QueryRow(`SELECT id FROM users ORDER BY created_at ASC LIMIT 1`).Scan(&firstID)
	return user.ID == firstID
}

func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isAdmin(c) {
			// Return 404 to hide admin endpoint existence
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func AdminStats(c *gin.Context) {
	var userCount, gameCount, reviewCount, sessionCount int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM games`).Scan(&gameCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM reviews`).Scan(&reviewCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE expires_at > datetime('now')`).Scan(&sessionCount)

	c.JSON(http.StatusOK, gin.H{
		"users":           userCount,
		"games":           gameCount,
		"reviews":         reviewCount,
		"active_sessions": sessionCount,
	})
}

func AdminListUsers(c *gin.Context) {
	rows, err := storage.DB.Query(
		`SELECT u.id, u.username, u.email, u.is_developer, u.created_at,
		        (SELECT COUNT(*) FROM games WHERE developer_id = u.id) as game_count,
		        (SELECT COUNT(*) FROM reviews WHERE user_id = u.id) as review_count
		 FROM users u ORDER BY u.created_at DESC`,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}
	defer rows.Close()

	type AdminUser struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		Email       string `json:"email"`
		IsDeveloper bool   `json:"is_developer"`
		CreatedAt   string `json:"created_at"`
		GameCount   int    `json:"game_count"`
		ReviewCount int    `json:"review_count"`
	}
	users := []AdminUser{}
	for rows.Next() {
		var u AdminUser
		rows.Scan(&u.ID, &u.Username, &u.Email, &u.IsDeveloper, &u.CreatedAt, &u.GameCount, &u.ReviewCount)
		users = append(users, u)
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

func AdminDeleteUser(c *gin.Context) {
	userID := c.Param("id")
	// Don't let admin delete themselves
	user := middleware.GetUser(c)
	if user.ID == userID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete yourself"})
		return
	}
	// Delete user's games files
	rows, _ := storage.DB.Query(`SELECT id FROM games WHERE developer_id = ?`, userID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var gameID string
			rows.Scan(&gameID)
			storage.DeleteGameFiles(gameID)
		}
	}
	// Cascade delete
	for _, table := range []string{"sessions", "activity", "reviews", "playtime", "library", "wishlist", "developer_pages", "devlogs", "follows", "collections", "games"} {
		col := "user_id"
		if table == "games" {
			col = "developer_id"
		}
		if table == "follows" {
			storage.DB.Exec(`DELETE FROM follows WHERE follower_id = ? OR followed_id = ?`, userID, userID)
			continue
		}
		storage.DB.Exec(`DELETE FROM `+table+` WHERE `+col+` = ?`, userID)
	}
	storage.DB.Exec(`DELETE FROM users WHERE id = ?`, userID)
	adminLog(c, "delete_user:"+userID)
	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}

func AdminDeleteGame(c *gin.Context) {
	gameID := c.Param("id")
	storage.DeleteGameFiles(gameID)
	storage.DB.Exec(`DELETE FROM games WHERE id = ?`, gameID)
	adminLog(c, "delete_game:"+gameID)
	c.JSON(http.StatusOK, gin.H{"message": "game deleted"})
}

func AdminListGames(c *gin.Context) {
	rows, err := storage.DB.Query(
		`SELECT g.id, g.title, g.genre, g.published, g.created_at, u.username,
		        (SELECT COUNT(*) FROM reviews WHERE game_id = g.id) as review_count
		 FROM games g JOIN users u ON g.developer_id = u.id ORDER BY g.created_at DESC`,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list games"})
		return
	}
	defer rows.Close()

	type AdminGame struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Genre       string `json:"genre"`
		Published   bool   `json:"published"`
		CreatedAt   string `json:"created_at"`
		Developer   string `json:"developer"`
		ReviewCount int    `json:"review_count"`
	}
	games := []AdminGame{}
	for rows.Next() {
		var g AdminGame
		rows.Scan(&g.ID, &g.Title, &g.Genre, &g.Published, &g.CreatedAt, &g.Developer, &g.ReviewCount)
		games = append(games, g)
	}
	c.JSON(http.StatusOK, gin.H{"games": games})
}

func AdminTogglePublish(c *gin.Context) {
	gameID := c.Param("id")
	storage.DB.Exec(`UPDATE games SET published = NOT published WHERE id = ?`, gameID)
	adminLog(c, "toggle_publish:"+gameID)
	c.JSON(http.StatusOK, gin.H{"message": "publish status toggled"})
}
