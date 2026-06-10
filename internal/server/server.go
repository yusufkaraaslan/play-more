package server

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// =============================================================================
// Server setup
// =============================================================================

func New(frontendFS embed.FS, goatCounterURL, gamesDomain, baseURL, trustedProxies string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.MaxMultipartMemory = 32 << 20

	// limitBody returns middleware that caps the request body to maxBytes.
	limitBody := func(maxBytes int64) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
			c.Next()
		}
	}
	uploadCap := int64(storage.MaxFileSize) + (32 << 20)
	imageCap := int64(10 << 20)

	// =========================================================================
	// Global middleware
	// =========================================================================

	// Custom logger — strips query strings so tokens don't land in logs.
	r.Use(gin.LoggerWithFormatter(func(p gin.LogFormatterParams) string {
		path := strings.ReplaceAll(p.Path, "\n", "")
		path = strings.ReplaceAll(path, "\r", "")
		return fmt.Sprintf("%s | %3d | %13v | %15s | %-7s %s\n",
			p.TimeStamp.Format("2006/01/02 - 15:04:05"),
			p.StatusCode, p.Latency, p.ClientIP, p.Method, path,
		)
	}))

	// Trust proxies
	hasTrustedProxy := false
	if trustedProxies != "" {
		proxies := []string{}
		for _, p := range strings.Split(trustedProxies, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if p == "0.0.0.0/0" || p == "::/0" {
				panic("trusted-proxies must not include 0.0.0.0/0 or ::/0")
			}
			proxies = append(proxies, p)
		}
		if len(proxies) > 0 {
			r.SetTrustedProxies(proxies)
			middleware.SetTrustedProxies(proxies)
			hasTrustedProxy = true
		} else {
			r.SetTrustedProxies(nil)
			middleware.SetTrustedProxies(nil)
		}
	} else {
		r.SetTrustedProxies(nil)
	}

	// HTTPS redirect (before security headers)
	r.Use(func(c *gin.Context) {
		if hasTrustedProxy && c.Request.Header.Get("X-Forwarded-Proto") == "http" {
			scheme := "https"
			host := c.Request.Host
			if baseURL != "" {
				if i := strings.Index(baseURL, "://"); i != -1 {
					host = baseURL[i+3:]
					scheme = baseURL[:i]
				}
			}
			target := scheme + "://" + host + c.Request.URL.Path
			if len(c.Request.URL.RawQuery) > 0 {
				target += "?" + c.Request.URL.RawQuery
			}
			c.Redirect(http.StatusMovedPermanently, target)
			c.Abort()
			return
		}
		c.Next()
	})

	r.Use(securityHeaders(goatCounterURL))
	r.Use(cacheHeaders())

	// JSON body size limit
	r.Use(func(c *gin.Context) {
		ct := c.GetHeader("Content-Type")
		if strings.Contains(ct, "application/json") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		}
		c.Next()
	})

	// Gzip (skip game file serving)
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/play/"})))

	// Site analytics
	r.Use(middleware.TrackPageView())

	// =========================================================================
	// Health checks
	// =========================================================================

	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/ready", func(c *gin.Context) {
		if err := storage.DB.Ping(); err != nil {
			c.JSON(503, gin.H{"status": "not ready", "error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "ready"})
	})

	// =========================================================================
	// API routes
	// =========================================================================

	api := r.Group("/api")
	api.Use(middleware.GlobalRateLimit(600, 300))
	api.Use(middleware.AuthOptional())
	// CSRF after auth so we can check auth_method (API keys skip CSRF)
	api.Use(middleware.CSRFProtect())
	{
		// Auth (strict rate limits)
		auth := api.Group("/auth")
		auth.GET("/captcha", middleware.RateLimit(60, 60), handlers.IssueCaptcha)
		auth.POST("/register", middleware.RateLimit(5, 3600), handlers.Register)
		auth.POST("/login", middleware.RateLimit(10, 300), handlers.Login)
		auth.POST("/logout", handlers.Logout)
		auth.GET("/me", handlers.Me)
		auth.POST("/verify", middleware.RateLimit(10, 3600), handlers.VerifyEmail)
		auth.POST("/forgot-password", middleware.RateLimit(5, 3600), handlers.ForgotPassword)
		auth.POST("/reset-password", middleware.RateLimit(10, 3600), handlers.ResetPassword)
		auth.POST("/resend-verification", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.ResendVerification)

		// Games
		api.GET("/games", handlers.ListGames)
		api.GET("/games/:id", handlers.GetGame)
		api.POST("/games", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(10, 3600), limitBody(uploadCap), handlers.UploadGame)
		api.PUT("/games/:id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(60, 3600), handlers.UpdateGame)
		api.DELETE("/games/:id", middleware.AuthRequired(), middleware.RateLimit(10, 3600), handlers.DeleteGame)
		api.POST("/games/:id/reupload", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(10, 3600), limitBody(uploadCap), handlers.ReuploadGameFiles)
		api.PUT("/games/:id/visibility", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(30, 3600), handlers.ToggleVisibility)
		api.POST("/games/:id/cover", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), limitBody((5<<20)+(1<<20)), handlers.UpdateCoverImage)
		api.POST("/games/:id/screenshots", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), limitBody(uploadCap), handlers.ManageScreenshots)
		api.DELETE("/games/:id/screenshots/:index", middleware.AuthRequired(), middleware.RateLimit(60, 3600), handlers.DeleteScreenshot)

		// Reviews
		api.GET("/games/:id/reviews", handlers.ListReviews)
		api.POST("/games/:id/reviews", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), handlers.CreateReview)
		api.DELETE("/reviews/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteReview)

		// Library
		api.GET("/library", middleware.AuthRequired(), handlers.GetLibrary)
		api.POST("/library/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.AddToLibrary)
		api.DELETE("/library/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.RemoveFromLibrary)

		// Wishlist
		api.GET("/wishlist", middleware.AuthRequired(), handlers.GetWishlist)
		api.POST("/wishlist/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.AddToWishlist)
		api.DELETE("/wishlist/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.RemoveFromWishlist)

		// Profile
		api.GET("/profile/:username", handlers.GetProfile)
		api.PUT("/profile", middleware.AuthRequired(), middleware.RateLimit(10, 300), handlers.UpdateProfile)
		api.GET("/activity", middleware.AuthRequired(), handlers.GetActivity)
		api.POST("/playtime", middleware.AuthRequired(), middleware.RateLimit(60, 60), handlers.RecordPlaytime)

		// Settings
		api.DELETE("/settings/account", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.DeleteAccount)
		api.PUT("/settings/password", middleware.AuthRequired(), middleware.RateLimit(5, 3600), handlers.ChangePassword)

		// Developer pages
		api.GET("/developer/:username", handlers.GetDeveloperPage)
		api.PUT("/developer", middleware.AuthRequired(), middleware.RateLimit(10, 300), handlers.UpdateDeveloperPage)
		api.GET("/developer/:username/games", handlers.GetDeveloperGames)

		// Achievements
		api.GET("/achievements/:username", handlers.GetUserAchievements)
		api.POST("/achievements/check", middleware.AuthRequired(), middleware.RateLimit(10, 300), handlers.CheckMyAchievements)

		// Analytics
		api.POST("/games/:id/view", middleware.RateLimit(60, 60), handlers.TrackView)
		api.POST("/analytics/client", middleware.RateLimit(60, 60), handlers.TrackClientInfo)
		api.GET("/games/:id/analytics", middleware.AuthRequired(), handlers.GetGameAnalytics)

		// Notifications
		api.GET("/notifications", middleware.AuthRequired(), handlers.GetNotifications)
		api.POST("/notifications/read", middleware.AuthRequired(), handlers.MarkNotificationsRead)

		// Feed (aggregated timeline)
		api.GET("/feed", middleware.AuthRequired(), handlers.GetFeed)

		// Devlogs
		api.GET("/games/:id/devlogs", handlers.ListDevlogs)
		api.POST("/games/:id/devlogs", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), handlers.CreateDevlog)
		api.DELETE("/devlogs/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteDevlog)

		// Comments on devlogs
		api.GET("/devlogs/:id/comments", handlers.ListComments)
		api.POST("/devlogs/:id/comments", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(30, 3600), handlers.CreateComment)
		api.DELETE("/comments/:id", middleware.AuthRequired(), middleware.RateLimit(60, 3600), handlers.DeleteComment)

		// Follows
		api.POST("/follow/:username", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.FollowDeveloper)
		api.DELETE("/follow/:username", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.UnfollowDeveloper)
		api.GET("/following", middleware.AuthRequired(), handlers.GetFollowing)
		api.GET("/followers/:username", handlers.GetFollowerCount)

		// Collections / Lists
		api.GET("/collections", middleware.AuthRequired(), handlers.ListCollections)
		api.GET("/collections/public", handlers.BrowsePublicLists)
		api.GET("/collections/:id", handlers.GetCollection)
		api.POST("/collections", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.CreateCollection)
		api.PUT("/collections/:id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.UpdateCollection)
		api.DELETE("/collections/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteCollection)
		api.POST("/collections/:id/games", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.AddToCollection)
		api.DELETE("/collections/:id/games/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.RemoveFromCollection)
	}

	// Admin routes — under the /api group so AuthOptional + CSRFProtect apply.
	admin := api.Group("/admin")
	admin.Use(handlers.AdminRequired())
	{
		admin.GET("/stats", middleware.RateLimit(120, 3600), handlers.AdminStats)
		admin.GET("/users", middleware.RateLimit(120, 3600), handlers.AdminListUsers)
		admin.DELETE("/users/:id", middleware.RateLimit(10, 3600), handlers.AdminDeleteUser)
		admin.GET("/games", middleware.RateLimit(120, 3600), handlers.AdminListGames)
		admin.DELETE("/games/:id", middleware.RateLimit(10, 3600), handlers.AdminDeleteGame)
		admin.PUT("/games/:id/publish", middleware.RateLimit(30, 3600), handlers.AdminTogglePublish)
		admin.GET("/analytics", middleware.RateLimit(120, 3600), handlers.AdminSiteAnalytics)
	}

	// Image uploads
	api.POST("/upload/image", middleware.AuthRequired(), middleware.RateLimit(20, 3600), limitBody(imageCap), handlers.UploadImage)

	// Chunked upload pipeline — see docs/superpowers/specs/2026-05-21-chunked-upload-design.md
	chunkPutCap := int64((8 << 20) + (1 << 20)) // 8 MiB chunk + 1 MiB headroom = 9 MiB
	api.POST("/uploads/init", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), limitBody(1<<20), handlers.InitUpload)
	api.PUT("/uploads/:upload_id/chunks", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(2000, 3600), limitBody(chunkPutCap), handlers.PutChunk)
	api.GET("/uploads/:upload_id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(600, 3600), handlers.GetUploadStatus)
	api.POST("/uploads/:upload_id/finalize", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), limitBody(1<<20), handlers.FinalizeUpload)
	api.DELETE("/uploads/:upload_id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(60, 3600), handlers.CancelUpload)

	// Serve uploaded images. r.Static would expose directory listings
	// (http.FileServer behavior); wrap with a handler that 404s any path
	// resolving to a directory so /uploads/<userID>/ doesn't enumerate files.
	uploadsDir := filepath.Join(storage.GamesDir, "..", "uploads")
	uploadsFS := http.Dir(uploadsDir)
	r.GET("/uploads/*filepath", func(c *gin.Context) {
		fp := c.Param("filepath")
		if fp == "" || strings.HasSuffix(fp, "/") {
			c.String(http.StatusNotFound, "not found")
			return
		}
		f, err := uploadsFS.Open(fp)
		if err != nil {
			c.String(http.StatusNotFound, "not found")
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			c.String(http.StatusNotFound, "not found")
			return
		}
		// Defense-in-depth: /uploads/ serves user-uploaded image content.
		// Image validation (UploadImage → ValidateImageBytes) rejects anything
		// that doesn't decode as PNG/JPG/GIF, but if that validation is ever
		// loosened or another code path drops a file here, these headers
		// prevent the file from being abused as a script-executing document.
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "default-src 'none'; img-src 'self' data: blob:; style-src 'unsafe-inline'; sandbox")
		c.Header("Cross-Origin-Opener-Policy", "same-origin")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		http.ServeContent(c.Writer, c.Request, stat.Name(), stat.ModTime(), f)
	})

	// Seed demo data (admin only)
	api.POST("/seed", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.SeedData)

	// API Keys
	api.GET("/api-keys", middleware.AuthRequired(), handlers.ListAPIKeysHandler)
	api.POST("/api-keys", middleware.AuthRequired(), middleware.RateLimit(10, 3600), handlers.CreateAPIKeyHandler)
	api.DELETE("/api-keys/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteAPIKeyHandler)

	// Self-hosted avatar generation
	r.GET("/avatar/:username", middleware.RateLimit(120, 60), handlers.GetAvatar)

	// API documentation
	r.GET("/docs", middleware.RateLimit(60, 60), handlers.APIDocs)

	// Deploy script download
	r.GET("/deploy.sh", middleware.RateLimit(10, 60), handlers.ServeDeployScript)

	// Game file serving (for iframe player)
	// Game iframe content. spaOrigin gates who can embed via CSP frame-ancestors —
	// XFO can't whitelist a cross-origin host, so split-origin (games.* subdomain) needs CSP.
	spaOrigin := strings.TrimRight(baseURL, "/")
	r.GET("/play/:id", handlers.ServeGameFiles(spaOrigin))
	r.GET("/play/:id/*filepath", handlers.ServeGameFiles(spaOrigin))

	// =========================================================================
	// Frontend (SPA)
	// =========================================================================

	frontendSub, err := fs.Sub(frontendFS, "frontend")
	if err == nil {
		r.StaticFS("/assets", http.FS(frontendSub))

		wellKnownSub, err := fs.Sub(frontendFS, "frontend/.well-known")
		if err == nil {
			r.StaticFS("/.well-known", http.FS(wellKnownSub))
		}

		r.NoRoute(spaFallback(frontendFS, goatCounterURL, gamesDomain, baseURL))
	}

	return r
}
