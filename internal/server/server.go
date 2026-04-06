package server

import (
	"bytes"
	"embed"
	"html"
	"io/fs"
	"net/http"
	"path/filepath"

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
		csp := "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; frame-src 'self'; media-src 'self' https://www.youtube.com"
		if goatCounterURL != "" {
			csp = "default-src 'self'; script-src 'self' 'unsafe-inline' https://gc.zgo.at; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https://gc.zgo.at; connect-src 'self' https://*.goatcounter.com; frame-src 'self'; media-src 'self' https://www.youtube.com"
		}
		c.Header("Content-Security-Policy", csp)
		if c.Request.Header.Get("X-Forwarded-Proto") == "https" || c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		c.Header("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=(), interest-cohort=()")
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

	// Site analytics tracking
	r.Use(middleware.TrackPageView())

	// CSRF protection for all state-changing requests
	r.Use(middleware.CSRFProtect())

	// API routes
	api := r.Group("/api")
	api.Use(middleware.AuthOptional())
	{
		// Auth (strict rate limits)
		auth := api.Group("/auth")
		auth.POST("/register", middleware.RateLimit(5, 3600), handlers.Register)
		auth.POST("/login", middleware.RateLimit(10, 300), handlers.Login)
		auth.POST("/logout", handlers.Logout)
		auth.GET("/me", handlers.Me)

		// Games
		api.GET("/games", handlers.ListGames)
		api.GET("/games/:id", handlers.GetGame)
		api.POST("/games", middleware.AuthRequired(), middleware.RateLimit(10, 3600), handlers.UploadGame)
		api.PUT("/games/:id", middleware.AuthRequired(), handlers.UpdateGame)
		api.DELETE("/games/:id", middleware.AuthRequired(), handlers.DeleteGame)

		// Reviews
		api.GET("/games/:id/reviews", handlers.ListReviews)
		api.POST("/games/:id/reviews", middleware.AuthRequired(), middleware.RateLimit(20, 3600), handlers.CreateReview)
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
		api.POST("/games/:id/devlogs", middleware.AuthRequired(), handlers.CreateDevlog)
		api.DELETE("/devlogs/:id", middleware.AuthRequired(), handlers.DeleteDevlog)

		// Comments on devlogs
		api.GET("/devlogs/:id/comments", handlers.ListComments)
		api.POST("/devlogs/:id/comments", middleware.AuthRequired(), handlers.CreateComment)
		api.DELETE("/comments/:id", middleware.AuthRequired(), handlers.DeleteComment)

		// Follows
		api.POST("/follow/:username", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.FollowDeveloper)
		api.DELETE("/follow/:username", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.UnfollowDeveloper)
		api.GET("/following", middleware.AuthRequired(), handlers.GetFollowing)
		api.GET("/followers/:username", handlers.GetFollowerCount)

		// Collections
		api.GET("/collections", middleware.AuthRequired(), handlers.ListCollections)
		api.POST("/collections", middleware.AuthRequired(), handlers.CreateCollection)
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
			// Inject GoatCounter script if configured
			if goatCounterURL != "" {
				escaped := html.EscapeString(goatCounterURL)
				snippet := []byte(`<script data-goatcounter="` + escaped + `/count" async src="` + escaped + `/count.js"></script></body>`)
				data = bytes.Replace(data, []byte("</body>"), snippet, 1)
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
		})
	}

	return r
}
