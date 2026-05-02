package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func DeleteAccount(c *gin.Context) {
	if middleware.IsAPIKeyAuth(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "not allowed with API key"})
		return
	}
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	// Re-auth: irreversible action requires the current password to be re-typed.
	// CSRF protects against cross-site triggers, but not against XSS, an
	// unattended laptop, or a stolen session cookie.
	var input struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "current password required to delete account"})
		return
	}
	if !user.CheckPassword(input.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}

	// Refuse to delete the admin (lowest-created_at user) — would silently
	// promote the next-oldest user. They must hand off admin first.
	var firstID string
	storage.DB.QueryRow(`SELECT id FROM users ORDER BY created_at ASC LIMIT 1`).Scan(&firstID)
	if firstID == user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "the instance admin account cannot be deleted from here"})
		return
	}

	// Delete all user data (CASCADE handles most)
	storage.DB.Exec(`DELETE FROM api_keys WHERE user_id = ?`, user.ID)
	storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ?`, user.ID)
	storage.DB.Exec(`DELETE FROM activity WHERE user_id = ?`, user.ID)
	storage.DB.Exec(`DELETE FROM reviews WHERE user_id = ?`, user.ID)
	storage.DB.Exec(`DELETE FROM playtime WHERE user_id = ?`, user.ID)
	storage.DB.Exec(`DELETE FROM library WHERE user_id = ?`, user.ID)
	storage.DB.Exec(`DELETE FROM wishlist WHERE user_id = ?`, user.ID)
	storage.DB.Exec(`DELETE FROM developer_pages WHERE user_id = ?`, user.ID)

	// Delete user's games and their files
	rows, _ := storage.DB.Query(`SELECT id FROM games WHERE developer_id = ?`, user.ID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var gameID string
			rows.Scan(&gameID)
			storage.DeleteGameFiles(gameID)
		}
	}
	storage.DB.Exec(`DELETE FROM games WHERE developer_id = ?`, user.ID)

	// Delete user
	storage.DB.Exec(`DELETE FROM users WHERE id = ?`, user.ID)

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", "", -1, "/", "", middleware.IsSecure(c), true)
	c.JSON(http.StatusOK, gin.H{"message": "account deleted"})
}

type changePasswordInput struct {
	Current string `json:"current" binding:"required"`
	New     string `json:"new" binding:"required,min=10"`
}

func ChangePassword(c *gin.Context) {
	if middleware.IsAPIKeyAuth(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "not allowed with API key"})
		return
	}
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input changePasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "current and new password (min 10 chars) required"})
		return
	}

	if !user.CheckPassword(input.Current) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.New), models.BcryptCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	if _, err := storage.DB.Exec(`UPDATE users SET password = ? WHERE id = ?`, string(hash), user.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save password"})
		return
	}

	// Invalidate all OTHER sessions (keep the current one alive so user stays logged in)
	currentToken, _ := c.Cookie("session")
	if currentToken != "" {
		storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ? AND token != ?`, user.ID, currentToken)
	} else {
		storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ?`, user.ID)
	}

	c.JSON(http.StatusOK, gin.H{"message": "password changed"})
}
