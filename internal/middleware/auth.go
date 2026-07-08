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

// AuthRequired rejects unauthenticated requests. Game-scoped credentials
// (pm_gk_, pm_gs_) are denied — they may only be used against game-scoped
// endpoints, not account/mutation routes.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			c.Abort()
			return
		}
		if IsGameAuth(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": "this credential cannot access account endpoints"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// AuthOptional loads user from Bearer token or session cookie.
// Three Bearer prefixes are recognized:
//   - "pm_k_"  — user-scoped API key. Sets auth_method = "api_key".
//   - "pm_gk_" — game-scoped long-lived key. Sets auth_method = "game_api_key".
//   - "pm_gs_" — short-lived runtime token. Sets auth_method = "game_session".
func AuthOptional() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		// 1. User-scoped API key.
		if strings.HasPrefix(authHeader, "Bearer pm_k_") {
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

		// 2. Game-scoped long-lived key.
		if strings.HasPrefix(authHeader, "Bearer pm_gk_") {
			rawKey := strings.TrimPrefix(authHeader, "Bearer ")
			gameKey, err := models.ValidateGameAPIKey(rawKey)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
				c.Abort()
				return
			}
			dev, derr := models.GetDeveloperByGameID(gameKey.GameID)
			if derr != nil || dev == nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
				c.Abort()
				return
			}
			c.Set(UserKey, dev)
			c.Set("game_api_key", gameKey)
			c.Set("auth_method", "game_api_key")
			c.Next()
			return
		}

		// 3. Game session token (short-lived, minted by SPA).
		if strings.HasPrefix(authHeader, "Bearer pm_gs_") {
			rawToken := strings.TrimPrefix(authHeader, "Bearer ")
			sessionTok, user, err := models.ValidateGameSessionToken(rawToken)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session token"})
				c.Abort()
				return
			}
			c.Set(UserKey, user)
			c.Set("game_session_token", sessionTok)
			c.Set("auth_method", "game_session")
			c.Next()
			return
		}

		// 4. Fall back to session cookie.
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

// AuthRequiredOrGameSession accepts session cookies, pm_k_ API keys,
// and pm_gs_ game session tokens. Used by the WebSocket route so that
// a game iframe can connect directly with a pm_gs_ token (the SPA
// mints it and passes it via postMessage). Game API keys (pm_gk_)
// are still rejected — those are for server-side logic, not browser
// WebSocket connections.
func AuthRequiredOrGameSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			c.Abort()
			return
		}
		// pm_gk_ keys are for server-side logic only, not browser WS.
		if IsGameAPIKeyAuth(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": "game API keys cannot connect to WebSocket"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// IsAPIKeyAuth returns true if the request was authenticated via
// a user-scoped pm_k_ API key.
func IsAPIKeyAuth(c *gin.Context) bool {
	method, _ := c.Get("auth_method")
	return method == "api_key"
}

// IsGameAuth reports whether the request is authenticated by a
// game-scoped credential (pm_gk_ or pm_gs_).
func IsGameAuth(c *gin.Context) bool {
	method, _ := c.Get("auth_method")
	return method == "game_api_key" || method == "game_session"
}

// IsGameAPIKeyAuth returns true for pm_gk_ keys.
func IsGameAPIKeyAuth(c *gin.Context) bool {
	method, _ := c.Get("auth_method")
	return method == "game_api_key"
}

// IsGameSessionAuth returns true for pm_gs_ tokens.
func IsGameSessionAuth(c *gin.Context) bool {
	method, _ := c.Get("auth_method")
	return method == "game_session"
}

// GetGameAPIKey returns the pm_gk_ key attached by AuthOptional, or nil.
func GetGameAPIKey(c *gin.Context) *models.GameAPIKey {
	val, exists := c.Get("game_api_key")
	if !exists {
		return nil
	}
	key, ok := val.(*models.GameAPIKey)
	if !ok {
		return nil
	}
	return key
}

// GetGameSessionToken returns the pm_gs_ token attached by AuthOptional, or nil.
func GetGameSessionToken(c *gin.Context) *models.GameSessionToken {
	val, exists := c.Get("game_session_token")
	if !exists {
		return nil
	}
	tok, ok := val.(*models.GameSessionToken)
	if !ok {
		return nil
	}
	return tok
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

// WSQueryTokenAuth copies a ?token=pm_gs_... query parameter into the
// Authorization header so AuthOptional can process it. Browsers cannot
// set custom headers on WebSocket handshakes, so the game iframe passes
// its pm_gs_ token via the query string. This middleware is safe to use
// only on the /ws route — the token is short-lived (5 min) and the WS
// handler has its own Origin check.
func WSQueryTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			if token := c.Query("token"); strings.HasPrefix(token, "pm_gs_") {
				c.Request.Header.Set("Authorization", "Bearer "+token)
			}
		}
		c.Next()
	}
}
