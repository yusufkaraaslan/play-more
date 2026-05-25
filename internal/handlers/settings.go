package handlers

import (
	"log"
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

	// Wrap in a transaction so all deletes are atomic. If the user's games and
	// files are deleted but one of the subsequent DELETEs fails, state is
	// inconsistent (orphaned data or a partially-cleaned account).
	tx, err := storage.DB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	defer tx.Rollback()

	// Delete all user data (CASCADE handles most, but some tables lack FKs)
	tx.Exec(`DELETE FROM api_keys WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM activity WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM reviews WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM playtime WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM library WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM wishlist WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM developer_pages WHERE user_id = ?`, user.ID)
	// Tables without CASCADE FK constraints
	tx.Exec(`DELETE FROM game_views WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM page_views WHERE user_id = ?`, user.ID)
	tx.Exec(`DELETE FROM follows WHERE follower_id = ? OR followed_id = ?`, user.ID, user.ID)
	tx.Exec(`DELETE FROM notifications WHERE user_id = ? OR from_user = ?`, user.ID, user.Username)

	// Collect game IDs for post-commit file cleanup. Don't delete files yet —
	// if the tx rolls back, files-gone + db-rows-kept = inconsistent state.
	gameIDs := []string{}
	rows, err := tx.Query(`SELECT id FROM games WHERE developer_id = ?`, user.ID)
	if err != nil {
		log.Printf("delete_account: list games failed for %s: %v", user.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete account"})
		return
	}
	for rows.Next() {
		var gameID string
		if err := rows.Scan(&gameID); err == nil {
			gameIDs = append(gameIDs, gameID)
		}
	}
	rows.Close()
	tx.Exec(`DELETE FROM games WHERE developer_id = ?`, user.ID)

	// Delete user
	tx.Exec(`DELETE FROM users WHERE id = ?`, user.ID)

	// Audit log
	tx.Exec(
		`INSERT INTO audit_log (actor_id, action, target_type, target_id, ip) VALUES (?, ?, ?, ?, ?)`,
		user.ID, "delete_account", "user", user.ID, middleware.RealClientIP(c),
	)

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete account"})
		return
	}

	// After commit succeeds, delete files. Failures here leave orphan files
	// on disk but the DB is consistent — uploads-GC will sweep them later.
	for _, gameID := range gameIDs {
		if err := storage.DeleteGameFiles(gameID); err != nil {
			log.Printf("delete_account: delete files for game %s: %v", gameID, err)
		}
	}

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

	// Invalidate all OTHER sessions (keep the current one alive so user stays logged in).
	// The sessions table stores SHA-256 hashes of tokens, not the raw values;
	// compare hashes, not the raw cookie, otherwise the != never matches and
	// we delete EVERY session (including the one we're trying to preserve).
	currentToken, _ := c.Cookie("session")
	if currentToken != "" {
		storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ? AND token != ?`, user.ID, models.HashSessionToken(currentToken))
	} else {
		storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ?`, user.ID)
	}

	// Audit log
	storage.DB.Exec(
		`INSERT INTO audit_log (actor_id, action, target_type, target_id, ip) VALUES (?, ?, ?, ?, ?)`,
		user.ID, "change_password", "user", user.ID, middleware.RealClientIP(c),
	)

	c.JSON(http.StatusOK, gin.H{"message": "password changed"})
}
