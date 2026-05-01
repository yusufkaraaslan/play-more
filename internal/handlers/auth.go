package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/yusufkaraaslan/play-more/internal/email"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type registerInput struct {
	Username string `json:"username" binding:"required,min=3,max=30"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=10"`
}

// usernameRe is a strict allowlist for usernames — alphanumeric, underscore, hyphen only.
// Prevents XSS via username in JS-string-in-HTML-attribute contexts.
var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,30}$`)

// reservedUsernames are usernames that collide with route names, system roles,
// or are commonly impersonated. Checked case-insensitively.
var reservedUsernames = map[string]bool{
	"admin": true, "administrator": true, "root": true, "system": true, "support": true,
	"playmore": true, "staff": true, "moderator": true, "mod": true, "official": true,
	"help": true, "security": true, "abuse": true, "noreply": true, "no-reply": true,
	"api": true, "auth": true, "login": true, "logout": true, "register": true,
	"signup": true, "settings": true, "profile": true, "developer": true, "store": true,
	"library": true, "wishlist": true, "feed": true, "search": true, "docs": true,
	"play": true, "uploads": true, "assets": true, "avatar": true, "deploy": true,
	"seed": true, "verify": true, "reset": true, "me": true, "you": true, "null": true,
	"undefined": true, "true": true, "false": true,
}

// IsValidUsername checks the regex AND the reserved-name list.
// Use everywhere a username is accepted (register, update).
func IsValidUsername(name string) bool {
	if !usernameRe.MatchString(name) {
		return false
	}
	return !reservedUsernames[strings.ToLower(name)]
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

	// Per-email register cap stops attackers rotating IPs to spam a victim's
	// inbox via the welcome/verification email path during signup.
	if !middleware.AllowByKey("register:"+input.Email, 3, 3600) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many registration attempts for this email, please wait"})
		return
	}

	if !IsValidUsername(input.Username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username must be 3-30 characters (letters, numbers, underscores, hyphens) and not a reserved name."})
		return
	}

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
			hashToken(vToken), user.ID, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
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
	emailKey := strings.ToLower(strings.TrimSpace(input.Email))

	// Per-account lockout — defeats distributed credential-stuffing where each
	// IP only hits the per-IP limit (10/5min) but thousands of IPs converge on
	// one account. 10 attempts per email per 15min, regardless of source IP.
	if !middleware.AllowByKey("login:"+emailKey, 10, 900) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts for this account, please try again later"})
		return
	}

	user, err := models.GetUserByEmail(emailKey)
	if err != nil {
		// Run a dummy bcrypt comparison to equalize timing with the
		// password-mismatch path, preventing user enumeration via timing.
		bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ012"),
			[]byte(input.Password),
		)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !user.CheckPassword(input.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	// Invalidate any pre-seated/orphan sessions for this user before issuing a
	// new one — defeats session fixation where an attacker plants a session
	// cookie that survives the victim's login.
	storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ?`, user.ID)

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

// hashToken returns a stable hex-encoded SHA-256 hash for storing email tokens
// in the DB. The raw token is sent in email; lookups hash and compare.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
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
	// Per-account verify-resend cap (in addition to per-IP rate-limit middleware).
	if !middleware.AllowByKey("verify:"+user.ID, 3, 3600) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many verification emails sent recently, please wait"})
		return
	}
	storage.DB.Exec(`DELETE FROM email_tokens WHERE user_id = ? AND type = 'verify'`, user.ID)
	token := generateToken()
	storage.DB.Exec(`INSERT INTO email_tokens (token, user_id, type, expires_at) VALUES (?, ?, 'verify', ?)`,
		hashToken(token), user.ID, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	go email.SendVerification(user.Email, user.Username, token)
	c.JSON(http.StatusOK, gin.H{"message": "verification email sent"})
}

// VerifyEmail validates a verification token (sent in JSON body) and marks email verified.
// POST keeps the token out of access logs / Referer headers.
func VerifyEmail(c *gin.Context) {
	var input struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}
	tokenHash := hashToken(input.Token)
	var userID, expiresAt string
	err := storage.DB.QueryRow(
		`SELECT user_id, expires_at FROM email_tokens WHERE token = ? AND type = 'verify'`, tokenHash,
	).Scan(&userID, &expiresAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired token"})
		return
	}
	exp, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(exp) {
		storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, tokenHash)
		c.JSON(http.StatusBadRequest, gin.H{"error": "token expired"})
		return
	}
	if _, err := storage.DB.Exec(`UPDATE users SET email_verified = 1 WHERE id = ?`, userID); err != nil {
		log.Printf("verify: failed to mark verified: %v", err)
	}
	storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, tokenHash)
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
	emailKey := strings.ToLower(strings.TrimSpace(input.Email))

	// Per-email cap stops attackers rotating IPs to flood a victim's inbox.
	// Generic success response is preserved either way (no enumeration).
	if !middleware.AllowByKey("reset:"+emailKey, 3, 3600) {
		c.JSON(http.StatusOK, gin.H{"message": "if an account exists, a reset email has been sent"})
		return
	}

	// Always return success to prevent user enumeration
	user, err := models.GetUserByEmail(emailKey)
	if err != nil || !email.Configured() {
		c.JSON(http.StatusOK, gin.H{"message": "if an account exists, a reset email has been sent"})
		return
	}
	// Delete old reset tokens for this user
	storage.DB.Exec(`DELETE FROM email_tokens WHERE user_id = ? AND type = 'reset'`, user.ID)
	token := generateToken()
	storage.DB.Exec(`INSERT INTO email_tokens (token, user_id, type, expires_at) VALUES (?, ?, 'reset', ?)`,
		hashToken(token), user.ID, time.Now().Add(1*time.Hour).UTC().Format(time.RFC3339))
	go email.SendPasswordReset(user.Email, user.Username, token)
	c.JSON(http.StatusOK, gin.H{"message": "if an account exists, a reset email has been sent"})
}

// ResetPassword validates the reset token and sets a new password.
func ResetPassword(c *gin.Context) {
	var input struct {
		Token    string `json:"token" binding:"required"`
		Password string `json:"password" binding:"required,min=10"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token and password (min 10 chars) required"})
		return
	}
	tokenHash := hashToken(input.Token)
	var userID, expiresAt string
	err := storage.DB.QueryRow(
		`SELECT user_id, expires_at FROM email_tokens WHERE token = ? AND type = 'reset'`, tokenHash,
	).Scan(&userID, &expiresAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired token"})
		return
	}
	exp, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(exp) {
		storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, tokenHash)
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
	storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, tokenHash)
	// Invalidate all sessions
	storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	c.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}
