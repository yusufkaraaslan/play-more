package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

func New(frontendFS embed.FS) *gin.Engine {
	r := gin.Default()

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
		api.PUT("/profile", middleware.AuthRequired(), handlers.UpdateProfile)
		api.GET("/activity", middleware.AuthRequired(), handlers.GetActivity)
		api.POST("/playtime", middleware.AuthRequired(), handlers.RecordPlaytime)

		// Settings
		api.DELETE("/settings/account", middleware.AuthRequired(), handlers.DeleteAccount)
		api.PUT("/settings/password", middleware.AuthRequired(), handlers.ChangePassword)

		// Developer pages
		api.GET("/developer/:username", handlers.GetDeveloperPage)
		api.PUT("/developer", middleware.AuthRequired(), handlers.UpdateDeveloperPage)
		api.GET("/developer/:username/games", handlers.GetDeveloperGames)

		// Analytics
		api.POST("/games/:id/view", handlers.TrackView)
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

		// Follows
		api.POST("/follow/:username", middleware.AuthRequired(), handlers.FollowDeveloper)
		api.DELETE("/follow/:username", middleware.AuthRequired(), handlers.UnfollowDeveloper)
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
		admin.DELETE("/users/:id", handlers.AdminDeleteUser)
		admin.GET("/games", handlers.AdminListGames)
		admin.DELETE("/games/:id", handlers.AdminDeleteGame)
		admin.PUT("/games/:id/publish", handlers.AdminTogglePublish)
	}

	// Image uploads
	api.POST("/upload/image", middleware.AuthRequired(), handlers.UploadImage)

	// Serve uploaded images
	uploadsDir := filepath.Join(storage.GamesDir, "..", "uploads")
	r.Static("/uploads", uploadsDir)

	// Seed demo data
	r.POST("/api/seed", handlers.SeedData)

	// Game file serving (for iframe player)
	r.GET("/play/:id", handlers.ServeGameFiles)
	r.GET("/play/:id/*filepath", handlers.ServeGameFiles)

	// Frontend (SPA) - serve embedded files
	frontendSub, err := fs.Sub(frontendFS, "frontend")
	if err == nil {
		r.StaticFS("/assets", http.FS(frontendSub))

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
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
		})
	}

	return r
}
