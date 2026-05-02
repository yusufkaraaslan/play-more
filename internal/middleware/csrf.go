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

		// If Origin is present, validate it matches (case-insensitive, port-normalized)
		if origin != "" {
			if !hostsMatch(extractHost(origin), host) {
				c.JSON(http.StatusForbidden, gin.H{"error": "cross-origin request blocked"})
				c.Abort()
				return
			}
			c.Next()
			return
		}

		// Fall back to Referer check
		if referer != "" {
			if !hostsMatch(extractHost(referer), host) {
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
	// Strip user-info if present (browsers don't normally send it, but defensive)
	if i := strings.LastIndex(urlStr, "@"); i != -1 {
		urlStr = urlStr[i+1:]
	}
	return urlStr
}

// hostsMatch compares two host strings tolerating case differences and
// default-port presence (e.g. "Example.com" matches "example.com:443" if
// we strip 443/80 since those are HTTPS/HTTP defaults).
func hostsMatch(a, b string) bool {
	return strings.EqualFold(stripDefaultPort(a), stripDefaultPort(b))
}

func stripDefaultPort(host string) string {
	if strings.HasSuffix(host, ":443") {
		return host[:len(host)-4]
	}
	if strings.HasSuffix(host, ":80") {
		return host[:len(host)-3]
	}
	return host
}
