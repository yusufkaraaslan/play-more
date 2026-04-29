package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CSRFProtect rejects state-changing requests that don't come from the same origin.
// Works by checking that the Origin or Referer header matches the Host.
// Browsers always send Origin on cross-origin requests, making this effective against CSRF.
func CSRFProtect() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only check state-changing methods
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// Skip CSRF for API key auth (non-browser clients don't send Origin/Referer).
		// Safe because AuthOptional rejects invalid Bearer tokens before we reach here,
		// and only valid API key auth sets this context value.
		if method, exists := c.Get("auth_method"); exists && method == "api_key" {
			c.Next()
			return
		}

		// Check Content-Type — reject form submissions (CSRF vector)
		// Our API only accepts JSON, so this blocks cross-origin form posts
		ct := c.GetHeader("Content-Type")
		origin := c.GetHeader("Origin")
		referer := c.GetHeader("Referer")
		host := c.Request.Host

		// If Origin is present, validate it matches
		if origin != "" {
			originHost := extractHost(origin)
			if originHost != host {
				c.JSON(http.StatusForbidden, gin.H{"error": "cross-origin request blocked"})
				c.Abort()
				return
			}
			c.Next()
			return
		}

		// Fall back to Referer check
		if referer != "" {
			refererHost := extractHost(referer)
			if refererHost != host {
				c.JSON(http.StatusForbidden, gin.H{"error": "cross-origin request blocked"})
				c.Abort()
				return
			}
			c.Next()
			return
		}

		// If neither Origin nor Referer is present:
		// - JSON is safe (cross-origin form posts can't send application/json without preflight)
		// - multipart/form-data is a CORS-simple type, so we must NOT allow it without Origin/Referer
		if !strings.Contains(ct, "application/json") {
			c.JSON(http.StatusForbidden, gin.H{"error": "missing origin header"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func extractHost(urlStr string) string {
	// Strip scheme (http:// or https://)
	if i := strings.Index(urlStr, "://"); i != -1 {
		urlStr = urlStr[i+3:]
	}
	// Strip path
	if i := strings.Index(urlStr, "/"); i != -1 {
		urlStr = urlStr[:i]
	}
	return urlStr
}
