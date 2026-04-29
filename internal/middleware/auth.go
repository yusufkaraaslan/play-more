package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

const UserKey = "user"

// ForceSecureCookies — when true, all session cookies have Secure=true regardless
// of the current request's TLS state. Set this when running behind a TLS-terminating
// reverse proxy where the front edge is always HTTPS.
var ForceSecureCookies bool

// IsSecure returns true if cookies should be marked Secure.
// True if: ForceSecureCookies is set, request is over TLS directly, or
// X-Forwarded-Proto: https is set (only trusted when SetTrustedProxies is configured).
func IsSecure(c *gin.Context) bool {
	if ForceSecureCookies {
		return true
	}
	if c.Request.TLS != nil {
		return true
	}
	// X-Forwarded-Proto is only set by Gin into c.Request when proxy is trusted
	return c.Request.Header.Get("X-Forwarded-Proto") == "https"
}

// AuthRequired rejects unauthenticated requests.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// AuthOptional loads user from Bearer token or session cookie.
func AuthOptional() gin.HandlerFunc {
	return func(c *gin.Context) {
			// 1. Try Bearer API key first — reject immediately if token is present but invalid
		if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bearer pm_k_") {
			rawKey := strings.TrimPrefix(authHeader, "Bearer ")
			user, apiKey, err := models.ValidateAPIKey(rawKey)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
				c.Abort()
				return
			}
			c.Set(UserKey, user)
			c.Set("api_key", apiKey)
			c.Set("auth_method", "api_key")
			c.Next()
			return
		}
		// 2. Fall back to session cookie
		token, err := c.Cookie("session")
		if err != nil || token == "" {
			c.Next()
			return
		}
		user, err := models.GetUserBySession(token)
		if err == nil && user != nil {
			c.Set(UserKey, user)
			c.Set("auth_method", "session")
		}
		c.Next()
	}
}

// IsAPIKeyAuth returns true if the request was authenticated via API key.
func IsAPIKeyAuth(c *gin.Context) bool {
	method, _ := c.Get("auth_method")
	return method == "api_key"
}

// GetUser returns the authenticated user or nil.
func GetUser(c *gin.Context) *models.User {
	val, exists := c.Get(UserKey)
	if !exists {
		// Try loading from cookie
		token, err := c.Cookie("session")
		if err != nil || token == "" {
			return nil
		}
		user, err := models.GetUserBySession(token)
		if err != nil {
			return nil
		}
		c.Set(UserKey, user)
		return user
	}
	user, ok := val.(*models.User)
	if !ok {
		return nil
	}
	return user
}
