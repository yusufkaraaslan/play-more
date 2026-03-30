package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

const UserKey = "user"

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

// AuthOptional loads user if session exists but doesn't reject.
func AuthOptional() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("session")
		if err != nil || token == "" {
			c.Next()
			return
		}
		user, err := models.GetUserBySession(token)
		if err == nil && user != nil {
			c.Set(UserKey, user)
		}
		c.Next()
	}
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
