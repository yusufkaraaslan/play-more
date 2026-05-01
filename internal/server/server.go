package server

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"html"
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

func New(frontendFS embed.FS, goatCounterURL, gamesDomain, baseURL, trustedProxies string) *gin.Engine {
	// gin.New() + Recovery() (no Logger) — we install a custom logger below
	// that strips query strings, so password-reset/verify tokens don't land
	// in journalctl forever.
	r := gin.New()
	r.Use(gin.Recovery())
	// Cap multipart parsing at 32 MiB in memory; larger uploads spill to tmp
	// (already capped per-route below). Caller must still wrap the request
	// body with http.MaxBytesReader for large endpoints.
	r.MaxMultipartMemory = 32 << 20

	// limitBody returns middleware that caps the request body to maxBytes.
	// Use on routes that accept multipart uploads — defense against attackers
	// stuffing huge non-target form fields past the per-file size check.
	limitBody := func(maxBytes int64) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
			c.Next()
		}
	}
	uploadCap := int64(storage.MaxFileSize) + (32 << 20) // game file + cover/screenshots/form overhead
	imageCap := int64(10 << 20)                          // 5 MiB image + form overhead
	r.Use(gin.LoggerWithFormatter(func(p gin.LogFormatterParams) string {
		// Path only, no RawQuery — tokens are passed via query in some flows.
		return fmt.Sprintf("%s | %3d | %13v | %15s | %-7s %s\n",
			p.TimeStamp.Format("2006/01/02 - 15:04:05"),
			p.StatusCode, p.Latency, p.ClientIP, p.Method, p.Path,
		)
	}))

	// Trust proxies — by default trust nothing; operator must explicitly opt in
	if trustedProxies != "" {
		proxies := []string{}
		for _, p := range strings.Split(trustedProxies, ",") {
			if p = strings.TrimSpace(p); p != "" {
				proxies = append(proxies, p)
			}
		}
		r.SetTrustedProxies(proxies)
	} else {
		r.SetTrustedProxies(nil)
	}

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
			// `preload` is intentionally omitted — submit the domain to
			// hstspreload.org first, then add it back. With `preload` set
			// without submission, a self-hoster who later drops TLS would
			// lock all returning visitors out for 2 years.
			c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
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
		auth.POST("/verify", middleware.RateLimit(10, 3600), handlers.VerifyEmail)
		auth.POST("/forgot-password", middleware.RateLimit(5, 3600), handlers.ForgotPassword)
		auth.POST("/reset-password", middleware.RateLimit(10, 3600), handlers.ResetPassword)
		auth.POST("/resend-verification", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.ResendVerification)

		// Games
		api.GET("/games", handlers.ListGames)
		api.GET("/games/:id", handlers.GetGame)
		api.POST("/games", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(10, 3600), limitBody(uploadCap), handlers.UploadGame)
		api.PUT("/games/:id", middleware.AuthRequired(), handlers.UpdateGame)
		api.DELETE("/games/:id", middleware.AuthRequired(), handlers.DeleteGame)
		api.POST("/games/:id/reupload", middleware.AuthRequired(), middleware.RateLimit(10, 3600), limitBody(uploadCap), handlers.ReuploadGameFiles)
		api.PUT("/games/:id/visibility", middleware.AuthRequired(), handlers.ToggleVisibility)
		api.POST("/games/:id/screenshots", middleware.AuthRequired(), middleware.RateLimit(20, 3600), limitBody(uploadCap), handlers.ManageScreenshots)
		api.DELETE("/games/:id/screenshots/:index", middleware.AuthRequired(), handlers.DeleteScreenshot)

		// Reviews
		api.GET("/games/:id/reviews", handlers.ListReviews)
		api.POST("/games/:id/reviews", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), handlers.CreateReview)
		api.DELETE("/reviews/:id", middleware.AuthRequired(), handlers.DeleteReview)

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
		api.DELETE("/devlogs/:id", middleware.AuthRequired(), handlers.DeleteDevlog)

		// Comments on devlogs
		api.GET("/devlogs/:id/comments", handlers.ListComments)
		api.POST("/devlogs/:id/comments", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(30, 3600), handlers.CreateComment)
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
		api.POST("/collections", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.CreateCollection)
		api.PUT("/collections/:id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.UpdateCollection)
		api.DELETE("/collections/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteCollection)
		api.POST("/collections/:id/games", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.AddToCollection)
		api.DELETE("/collections/:id/games/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.RemoveFromCollection)
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
	api.POST("/upload/image", middleware.AuthRequired(), middleware.RateLimit(20, 3600), limitBody(imageCap), handlers.UploadImage)

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
		http.ServeContent(c.Writer, c.Request, stat.Name(), stat.ModTime(), f)
	})

	// Seed demo data (admin only)
	api.POST("/seed", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.SeedData)

	// API Keys
	api.GET("/api-keys", middleware.AuthRequired(), handlers.ListAPIKeysHandler)
	api.POST("/api-keys", middleware.AuthRequired(), middleware.RateLimit(10, 3600), handlers.CreateAPIKeyHandler)
	api.DELETE("/api-keys/:id", middleware.AuthRequired(), handlers.DeleteAPIKeyHandler)

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

			// Inject games origin if configured
			gamesOrigin := ""
			if gamesDomain != "" {
				scheme := "https://"
				if c.Request.TLS == nil && c.Request.Header.Get("X-Forwarded-Proto") != "https" {
					scheme = "http://"
				}
				gamesOrigin = scheme + gamesDomain
				originSnippet := []byte(`<script nonce="` + nonce + `">window.PLAYMORE_GAMES_ORIGIN="` + html.EscapeString(gamesOrigin) + `";</script></head>`)
				data = bytes.Replace(data, []byte("</head>"), originSnippet, 1)
			}

			// Build nonce-based CSP — frame-src must include games origin if set
			gcURL, _ := c.Get("goatcounter_url")
			gcStr, _ := gcURL.(string)
			frameSrc := "'self' https://www.youtube.com"
			if gamesOrigin != "" {
				frameSrc += " " + gamesOrigin
			}
			csp := "default-src 'self'; script-src 'self' 'nonce-" + nonce + "'; script-src-attr 'unsafe-inline'; style-src 'self' 'nonce-" + nonce + "' https://fonts.googleapis.com; style-src-attr 'unsafe-inline'; img-src 'self' data: blob: https://img.youtube.com; connect-src 'self'; frame-src " + frameSrc + "; media-src 'self'; font-src 'self' https://fonts.googleapis.com https://fonts.gstatic.com; object-src 'none'; base-uri 'self'; form-action 'self'"
			if gcStr != "" {
				csp = "default-src 'self'; script-src 'self' 'nonce-" + nonce + "' https://gc.zgo.at https://*.goatcounter.com https://static.cloudflareinsights.com; script-src-attr 'unsafe-inline'; style-src 'self' 'nonce-" + nonce + "' https://fonts.googleapis.com; style-src-attr 'unsafe-inline'; img-src 'self' data: blob: https://img.youtube.com https://gc.zgo.at; connect-src 'self' https://*.goatcounter.com https://cloudflareinsights.com; frame-src " + frameSrc + "; media-src 'self'; font-src 'self' https://fonts.googleapis.com https://fonts.gstatic.com; object-src 'none'; base-uri 'self'; form-action 'self'"
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
