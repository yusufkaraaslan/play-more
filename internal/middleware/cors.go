package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS handles the cross-origin surface for game iframes.
//
// # Why * and not origin-echo
//
// Game iframes are always opaque-origin: the server's HTML CSP applies
// `sandbox allow-scripts allow-pointer-lock allow-popups allow-forms
// allow-modals` (no allow-same-origin) on every /play/<id>/*.html
// response (see internal/handlers/games.go). That makes a game's
// fetch() to /api/* carry Origin: null regardless of --games-domain
// or any split-origin config. A CORS policy that echoes a specific
// origin (or even a configured gamesOrigin) would reject the legitimate
// game call. Returning * is the only model that matches the real
// browser behavior.
//
// # Why * is safe
//
// The game-facing API auth is exclusively Bearer (pm_gk_ / pm_gs_),
// never cookies. The *-wildcard permits Authorization on
// non-credentialed requests — credentials: 'include' would invalidate
// the wildcard, but we never use that mode. A CSRF attacker can
// already attempt requests with no Origin or with arbitrary Origins;
// CORS is not the layer that defends against that — the token is.
// Bearer tokens are not auto-attached by the browser, so a CSRF
// attacker who doesn't have the raw token cannot complete the call.
//
// # Exclusions
//
// The account/credential surface stays off the cross-origin surface
// entirely: /auth/*, /admin/*, /seed, plus /settings/*, /api-keys*,
// and /profile*. They are SPA-shell only; the SPA's same-origin fetch
// with session cookies is the only intended caller. A game never calls
// these. The direct CORS risk is low (requests are non-credentialed —
// no cookie crosses origins — so an attacker still needs a Bearer
// token), but keeping them off the wildcard surface is defense in
// depth and keeps the cross-origin surface to exactly what games need.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// The account/credential surface stays on the SPA shell —
		// same-origin only. Match on both /api/ and /api/v1/ prefixes
		// since mountAPIRoutes registers the same routes on both.
		if strings.HasPrefix(path, "/api/auth/") ||
			strings.HasPrefix(path, "/api/v1/auth/") ||
			strings.HasPrefix(path, "/api/admin/") ||
			strings.HasPrefix(path, "/api/v1/admin/") ||
			strings.HasPrefix(path, "/api/settings/") ||
			strings.HasPrefix(path, "/api/v1/settings/") ||
			strings.HasPrefix(path, "/api/api-keys") ||
			strings.HasPrefix(path, "/api/v1/api-keys") ||
			strings.HasPrefix(path, "/api/profile") ||
			strings.HasPrefix(path, "/api/v1/profile") ||
			path == "/api/seed" || path == "/api/v1/seed" {
			c.Next()
			return
		}

		// Preflight: short-circuit with the headers and 204. The
		// actual handler chain (auth/CSRF) does not run for
		// OPTIONS — preflight is always safe to answer directly.
		if c.Request.Method == http.MethodOptions {
			setCORSHeaders(c)
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		// Actual request: set ACAO: * so the browser permits the
		// response to be read. The token (not the origin) is the
		// auth gate.
		setCORSHeaders(c)
		c.Next()
	}
}

func setCORSHeaders(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
	c.Header("Access-Control-Max-Age", "600")
}
