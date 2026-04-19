package server

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"html"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

func New(frontendFS embed.FS, goatCounterURL string) *gin.Engine {
	r := gin.Default()

	// HTTPS redirect middleware (before security headers)
	r.Use(func(c *gin.Context) {
		if c.Request.Header.Get("X-Forwarded-Proto") == "http" {
			target := "https://" + c.Request.Host + c.Request.URL.Path
			if len(c.Request.URL.RawQuery) > 0 {
				target += "?" + c.Request.URL.RawQuery
			}
			c.Redirect(http.StatusMovedPermanently, target)
			c.Abort()
			return
		}
		c.Next()
	})

	// Security headers
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// CSP is set per-request with nonce in the SPA handler; set a default for non-HTML
		c.Set("goatcounter_url", goatCounterURL)
		c.Header("Content-Security-Policy", "default-src 'self'; object-src 'none'")
		// The SPA handler overrides this with a nonce-based CSP
		if c.Request.Header.Get("X-Forwarded-Proto") == "https" || c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		c.Header("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
		c.Next()
	})

	// Cache-Control headers middleware
	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		// API routes: no-store, no-cache, must-revalidate
		if len(path) >= 4 && path[:4] == "/api" {
			c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		} else if len(path) >= 8 && path[:8] == "/assets/" {
			// Static assets: let browser cache (default behavior, no Cache-Control header)
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// HTML pages and other routes: no-cache
			c.Header("Cache-Control", "no-cache")
		}
		c.Next()
	})

	// Gzip compression (skip game file serving — handled separately with Range support)
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/play/"})))

	// Site analytics tracking
	r.Use(middleware.TrackPageView())

	// API routes
	api := r.Group("/api")
	api.Use(middleware.AuthOptional())
	// CSRF after auth so we can check auth_method (API keys skip CSRF)
	api.Use(middleware.CSRFProtect())
	{
		// Auth (strict rate limits)
		auth := api.Group("/auth")
		auth.POST("/register", middleware.RateLimit(5, 3600), handlers.Register)
		auth.POST("/login", middleware.RateLimit(10, 300), handlers.Login)
		auth.POST("/logout", handlers.Logout)
		auth.GET("/me", handlers.Me)
		auth.GET("/verify/:token", handlers.VerifyEmail)
		auth.POST("/forgot-password", middleware.RateLimit(5, 3600), handlers.ForgotPassword)
		auth.POST("/reset-password", middleware.RateLimit(10, 3600), handlers.ResetPassword)
		auth.POST("/resend-verification", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.ResendVerification)

		// Games
		api.GET("/games", handlers.ListGames)
		api.GET("/games/:id", handlers.GetGame)
		api.POST("/games", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(10, 3600), handlers.UploadGame)
		api.PUT("/games/:id", middleware.AuthRequired(), handlers.UpdateGame)
		api.DELETE("/games/:id", middleware.AuthRequired(), handlers.DeleteGame)
		api.POST("/games/:id/reupload", middleware.AuthRequired(), handlers.ReuploadGameFiles)
		api.PUT("/games/:id/visibility", middleware.AuthRequired(), handlers.ToggleVisibility)
		api.POST("/games/:id/screenshots", middleware.AuthRequired(), handlers.ManageScreenshots)
		api.DELETE("/games/:id/screenshots/:index", middleware.AuthRequired(), handlers.DeleteScreenshot)

		// Reviews
		api.GET("/games/:id/reviews", handlers.ListReviews)
		api.POST("/games/:id/reviews", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), handlers.CreateReview)
		api.DELETE("/reviews/:id", middleware.AuthRequired(), handlers.DeleteReview)

		// Library
		api.GET("/library", middleware.AuthRequired(), handlers.GetLibrary)
		api.POST("/library/:game_id", middleware.AuthRequired(), handlers.AddToLibrary)
		api.DELETE("/library/:game_id", middleware.AuthRequired(), handlers.RemoveFromLibrary)

		// Wishlist
		api.GET("/wishlist", middleware.AuthRequired(), handlers.GetWishlist)
		api.POST("/wishlist/:game_id", middleware.AuthRequired(), handlers.AddToWishlist)
		api.DELETE("/wishlist/:game_id", middleware.AuthRequired(), handlers.RemoveFromWishlist)

		// Profile
		api.GET("/profile/:username", handlers.GetProfile)
		api.PUT("/profile", middleware.AuthRequired(), middleware.RateLimit(10, 300), handlers.UpdateProfile)
		api.GET("/activity", middleware.AuthRequired(), handlers.GetActivity)
		api.POST("/playtime", middleware.AuthRequired(), handlers.RecordPlaytime)

		// Settings
		api.DELETE("/settings/account", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.DeleteAccount)
		api.PUT("/settings/password", middleware.AuthRequired(), middleware.RateLimit(5, 3600), handlers.ChangePassword)

		// Developer pages
		api.GET("/developer/:username", handlers.GetDeveloperPage)
		api.PUT("/developer", middleware.AuthRequired(), middleware.RateLimit(10, 300), handlers.UpdateDeveloperPage)
		api.GET("/developer/:username/games", handlers.GetDeveloperGames)

		// Achievements
		api.GET("/achievements/:username", handlers.GetUserAchievements)
		api.POST("/achievements/check", middleware.AuthRequired(), handlers.CheckMyAchievements)

		// Analytics
		api.POST("/games/:id/view", handlers.TrackView)
		api.POST("/analytics/client", handlers.TrackClientInfo)
		api.GET("/games/:id/analytics", middleware.AuthRequired(), handlers.GetGameAnalytics)

		// Notifications
		api.GET("/notifications", middleware.AuthRequired(), handlers.GetNotifications)
		api.POST("/notifications/read", middleware.AuthRequired(), handlers.MarkNotificationsRead)

		// Feed (aggregated timeline)
		api.GET("/feed", middleware.AuthRequired(), handlers.GetFeed)

		// Devlogs
		api.GET("/games/:id/devlogs", handlers.ListDevlogs)
		api.POST("/games/:id/devlogs", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), handlers.CreateDevlog)
		api.DELETE("/devlogs/:id", middleware.AuthRequired(), handlers.DeleteDevlog)

		// Comments on devlogs
		api.GET("/devlogs/:id/comments", handlers.ListComments)
		api.POST("/devlogs/:id/comments", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), handlers.CreateComment)
		api.DELETE("/comments/:id", middleware.AuthRequired(), handlers.DeleteComment)

		// Follows
		api.POST("/follow/:username", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.FollowDeveloper)
		api.DELETE("/follow/:username", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.UnfollowDeveloper)
		api.GET("/following", middleware.AuthRequired(), handlers.GetFollowing)
		api.GET("/followers/:username", handlers.GetFollowerCount)

		// Collections / Lists
		api.GET("/collections", middleware.AuthRequired(), handlers.ListCollections)
		api.GET("/collections/public", handlers.BrowsePublicLists)
		api.GET("/collections/:id", handlers.GetCollection)
		api.POST("/collections", middleware.AuthRequired(), handlers.CreateCollection)
		api.PUT("/collections/:id", middleware.AuthRequired(), handlers.UpdateCollection)
		api.DELETE("/collections/:id", middleware.AuthRequired(), handlers.DeleteCollection)
		api.POST("/collections/:id/games", middleware.AuthRequired(), handlers.AddToCollection)
		api.DELETE("/collections/:id/games/:game_id", middleware.AuthRequired(), handlers.RemoveFromCollection)
	}

	// Admin routes
	admin := r.Group("/api/admin")
	admin.Use(middleware.AuthOptional(), handlers.AdminRequired())
	{
		admin.GET("/stats", handlers.AdminStats)
		admin.GET("/users", handlers.AdminListUsers)
		admin.DELETE("/users/:id", middleware.RateLimit(10, 3600), handlers.AdminDeleteUser)
		admin.GET("/games", handlers.AdminListGames)
		admin.DELETE("/games/:id", middleware.RateLimit(10, 3600), handlers.AdminDeleteGame)
		admin.PUT("/games/:id/publish", handlers.AdminTogglePublish)
		admin.GET("/analytics", handlers.AdminSiteAnalytics)
	}

	// Image uploads
	api.POST("/upload/image", middleware.AuthRequired(), handlers.UploadImage)

	// Serve uploaded images
	uploadsDir := filepath.Join(storage.GamesDir, "..", "uploads")
	r.Static("/uploads", uploadsDir)

	// Seed demo data (admin only, or first-run when no users exist)
	r.POST("/api/seed", middleware.AuthOptional(), handlers.SeedData)

	// API Keys
	api.GET("/api-keys", middleware.AuthRequired(), handlers.ListAPIKeysHandler)
	api.POST("/api-keys", middleware.AuthRequired(), middleware.RateLimit(10, 3600), handlers.CreateAPIKeyHandler)
	api.DELETE("/api-keys/:id", middleware.AuthRequired(), handlers.DeleteAPIKeyHandler)

	// Self-hosted avatar generation
	r.GET("/avatar/:username", handlers.GetAvatar)

	// API documentation
	r.GET("/docs", handlers.APIDocs)

	// Deploy script download
	r.GET("/deploy.sh", middleware.RateLimit(10, 60), handlers.ServeDeployScript)

	// Game file serving (for iframe player)
	r.GET("/play/:id", handlers.ServeGameFiles)
	r.GET("/play/:id/*filepath", handlers.ServeGameFiles)

	// Frontend (SPA) - serve embedded files
	frontendSub, err := fs.Sub(frontendFS, "frontend")
	if err == nil {
		r.StaticFS("/assets", http.FS(frontendSub))

		// Serve .well-known directory for security.txt
		wellKnownSub, err := fs.Sub(frontendFS, "frontend/.well-known")
		if err == nil {
			r.StaticFS("/.well-known", http.FS(wellKnownSub))
		}

		// SPA fallback: serve index.html for all non-API, non-play routes
		r.NoRoute(func(c *gin.Context) {
			// Don't intercept API or play routes
			if len(c.Request.URL.Path) > 4 && c.Request.URL.Path[:4] == "/api" {
				c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
				return
			}
			if len(c.Request.URL.Path) > 5 && c.Request.URL.Path[:5] == "/play" {
				c.String(http.StatusNotFound, "game not found")
				return
			}

			data, err := frontendFS.ReadFile("frontend/index.html")
			if err != nil {
				c.String(http.StatusInternalServerError, "frontend not found")
				return
			}

			// Generate per-request nonce for CSP
			nonceBytes := make([]byte, 16)
			rand.Read(nonceBytes)
			nonce := base64.StdEncoding.EncodeToString(nonceBytes)

			// Inject nonce into inline style and script tags
			data = bytes.Replace(data, []byte("<style>"), []byte(`<style nonce="`+nonce+`">`), 1)
			data = bytes.Replace(data, []byte("<script>"), []byte(`<script nonce="`+nonce+`">`), 1)

			// Build nonce-based CSP
			gcURL, _ := c.Get("goatcounter_url")
			gcStr, _ := gcURL.(string)
			// Nonce protects <script>/<style> blocks; unsafe-inline on -attr allows onclick/style attributes
			csp := "default-src 'self'; script-src 'self' 'nonce-" + nonce + "'; script-src-attr 'unsafe-inline'; style-src 'self' 'nonce-" + nonce + "' https://fonts.googleapis.com; style-src-attr 'unsafe-inline'; img-src 'self' data: blob: https://img.youtube.com; connect-src 'self'; frame-src 'self' https://www.youtube.com; media-src 'self'; font-src 'self' https://fonts.googleapis.com https://fonts.gstatic.com; object-src 'none'; base-uri 'self'; form-action 'self'"
			if gcStr != "" {
				csp = "default-src 'self'; script-src 'self' 'nonce-" + nonce + "' https://gc.zgo.at https://*.goatcounter.com https://static.cloudflareinsights.com; script-src-attr 'unsafe-inline'; style-src 'self' 'nonce-" + nonce + "' https://fonts.googleapis.com; style-src-attr 'unsafe-inline'; img-src 'self' data: blob: https://img.youtube.com https://gc.zgo.at; connect-src 'self' https://*.goatcounter.com https://cloudflareinsights.com; frame-src 'self' https://www.youtube.com; media-src 'self'; font-src 'self' https://fonts.googleapis.com https://fonts.gstatic.com; object-src 'none'; base-uri 'self'; form-action 'self'"
			}
			c.Header("Content-Security-Policy", csp)

			// Inject GoatCounter script if configured
			if gcStr != "" {
				escaped := html.EscapeString(gcStr)
				snippet := []byte(`<script nonce="` + nonce + `" data-goatcounter="` + escaped + `/count" async src="//gc.zgo.at/count.js"></script></body>`)
				data = bytes.Replace(data, []byte("</body>"), snippet, 1)
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
		})
	}

	return r
}
