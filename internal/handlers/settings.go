package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func DeleteAccount(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	// Delete all user data (CASCADE handles most)
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
	New     string `json:"new" binding:"required,min=6"`
}

func ChangePassword(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input changePasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !user.CheckPassword(input.Current) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.New), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	storage.DB.Exec(`UPDATE users SET password = ? WHERE id = ?`, string(hash), user.ID)
	c.JSON(http.StatusOK, gin.H{"message": "password changed"})
}
