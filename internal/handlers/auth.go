package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/email"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type registerInput struct {
	Username string `json:"username" binding:"required,min=3,max=30"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

func Register(c *gin.Context) {
	var input registerInput
	if err := c.ShouldBindJSON(&input); err != nil {
		log.Printf("Validation error in Register: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input. Please check all fields and try again."})
		return
	}

	input.Username = strings.TrimSpace(input.Username)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))

	user, err := models.CreateUser(input.Username, input.Email, input.Password)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			// Return generic error to prevent user enumeration
			c.JSON(http.StatusConflict, gin.H{"error": "Registration failed. Please try again with different information."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	token, err := models.CreateSession(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", token, 30*24*3600, "/", "", middleware.IsSecure(c), true)

	// Send verification email if SMTP is configured
	if email.Configured() {
		vToken := generateToken()
		storage.DB.Exec(`INSERT INTO email_tokens (token, user_id, type, expires_at) VALUES (?, ?, 'verify', ?)`,
			vToken, user.ID, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
		go email.SendVerification(input.Email, input.Username, vToken)
	}

	c.JSON(http.StatusCreated, gin.H{"user": user})
}

type loginInput struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func Login(c *gin.Context) {
	var input loginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		log.Printf("Validation error in Login: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input. Please check all fields and try again."})
		return
	}

	user, err := models.GetUserByEmail(strings.ToLower(strings.TrimSpace(input.Email)))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !user.CheckPassword(input.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := models.CreateSession(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", token, 30*24*3600, "/", "", middleware.IsSecure(c), true)
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func Logout(c *gin.Context) {
	token, _ := c.Cookie("session")
	if token != "" {
		models.DeleteSession(token)
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", "", -1, "/", "", middleware.IsSecure(c), true)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

func Me(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	stats, _ := models.GetUserStats(user.ID)
	c.JSON(http.StatusOK, gin.H{"user": user, "stats": stats})
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// RequireVerifiedEmail is a middleware that rejects unverified users.
// Only enforced when SMTP is configured (otherwise users can't verify).
func RequireVerifiedEmail() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !email.Configured() {
			c.Next()
			return
		}
		user := middleware.GetUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			c.Abort()
			return
		}
		if !user.EmailVerified {
			c.JSON(http.StatusForbidden, gin.H{
				"error":              "Please verify your email address first. Check your inbox for the verification link.",
				"email_verification": "required",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// ResendVerification sends a new verification email to the current user.
func ResendVerification(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	if user.EmailVerified {
		c.JSON(http.StatusOK, gin.H{"message": "email already verified"})
		return
	}
	if !email.Configured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "email sending not configured"})
		return
	}
	storage.DB.Exec(`DELETE FROM email_tokens WHERE user_id = ? AND type = 'verify'`, user.ID)
	token := generateToken()
	storage.DB.Exec(`INSERT INTO email_tokens (token, user_id, type, expires_at) VALUES (?, ?, 'verify', ?)`,
		token, user.ID, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	go email.SendVerification(user.Email, user.Username, token)
	c.JSON(http.StatusOK, gin.H{"message": "verification email sent"})
}

// VerifyEmail validates a verification token and marks the user's email as verified.
func VerifyEmail(c *gin.Context) {
	token := c.Param("token")
	var userID string
	var expiresAt string
	err := storage.DB.QueryRow(
		`SELECT user_id, expires_at FROM email_tokens WHERE token = ? AND type = 'verify'`, token,
	).Scan(&userID, &expiresAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired token"})
		return
	}
	exp, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(exp) {
		storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, token)
		c.JSON(http.StatusBadRequest, gin.H{"error": "token expired"})
		return
	}
	storage.DB.Exec(`UPDATE users SET email_verified = 1 WHERE id = ?`, userID)
	storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, token)
	c.JSON(http.StatusOK, gin.H{"message": "email verified"})
}

// ForgotPassword sends a password reset email.
func ForgotPassword(c *gin.Context) {
	var input struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid email required"})
		return
	}
	// Always return success to prevent user enumeration
	user, err := models.GetUserByEmail(strings.ToLower(strings.TrimSpace(input.Email)))
	if err != nil || !email.Configured() {
		c.JSON(http.StatusOK, gin.H{"message": "if an account exists, a reset email has been sent"})
		return
	}
	// Delete old reset tokens for this user
	storage.DB.Exec(`DELETE FROM email_tokens WHERE user_id = ? AND type = 'reset'`, user.ID)
	token := generateToken()
	storage.DB.Exec(`INSERT INTO email_tokens (token, user_id, type, expires_at) VALUES (?, ?, 'reset', ?)`,
		token, user.ID, time.Now().Add(1*time.Hour).UTC().Format(time.RFC3339))
	go email.SendPasswordReset(user.Email, user.Username, token)
	c.JSON(http.StatusOK, gin.H{"message": "if an account exists, a reset email has been sent"})
}

// ResetPassword validates the reset token and sets a new password.
func ResetPassword(c *gin.Context) {
	var input struct {
		Token    string `json:"token" binding:"required"`
		Password string `json:"password" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token and password (min 6 chars) required"})
		return
	}
	var userID, expiresAt string
	err := storage.DB.QueryRow(
		`SELECT user_id, expires_at FROM email_tokens WHERE token = ? AND type = 'reset'`, input.Token,
	).Scan(&userID, &expiresAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired token"})
		return
	}
	exp, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(exp) {
		storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, input.Token)
		c.JSON(http.StatusBadRequest, gin.H{"error": "token expired"})
		return
	}
	user, err := models.GetUserByID(userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
		return
	}
	if err := user.SetPassword(input.Password); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set password"})
		return
	}
	storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, input.Token)
	// Invalidate all sessions
	storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	c.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}
