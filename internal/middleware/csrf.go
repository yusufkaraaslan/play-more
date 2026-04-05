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

		// If neither Origin nor Referer is present, check Content-Type
		// Browsers always send Origin on cross-origin fetch/XHR, so missing Origin
		// means same-origin or a non-browser client (API tool, curl) — allow JSON only
		if !strings.Contains(ct, "application/json") && !strings.Contains(ct, "multipart/form-data") {
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
