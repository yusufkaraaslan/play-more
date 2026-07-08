package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
)

// apiConfig holds the per-request body byte caps used by the API routes.
// Lifted out of New() so mountAPIRoutes can be called twice (once for the
// canonical /api/v1/ prefix, once for the legacy /api/ alias) without
// duplicating the cap arithmetic.
type apiConfig struct {
	uploadCap   int64 // single-shot game upload (file + multipart overhead)
	imageCap    int64 // inline image upload
	chunkPutCap int64 // chunked-upload PUT (chunk + headroom)
}

// bodyLimit returns middleware that caps the request body to maxBytes.
// Defined here (not in server.go) so the routes table is self-contained.
func bodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// mountAPIRoutes registers the full /api surface on the given group.
// It is called twice from New() — once on r.Group("/api/v1") (canonical,
// new endpoints land here) and once on r.Group("/api") (permanent alias
// for backward compatibility with pre-versioning clients). The caller
// is responsible for any router-level middleware (e.g. r.Use(gin.Logger())).
//
// Per-group middleware (GlobalRateLimit, AuthOptional, CSRFProtect) is
// applied here so both prefixes get identical protection without callers
// having to remember to add it.
func mountAPIRoutes(g *gin.RouterGroup, cfg apiConfig) {
	g.Use(middleware.GlobalRateLimit(600, 300))
	g.Use(middleware.AuthOptional())
	// CSRF after auth so we can check auth_method (API keys skip CSRF)
	g.Use(middleware.CSRFProtect())

	// Auth (strict rate limits)
	auth := g.Group("/auth")
	auth.GET("/captcha", middleware.RateLimit(60, 60), handlers.IssueCaptcha)
	auth.POST("/register", middleware.RateLimit(5, 3600), handlers.Register)
	auth.POST("/login", middleware.RateLimit(10, 300), handlers.Login)
	auth.POST("/logout", handlers.Logout)
	auth.GET("/me", middleware.AuthRequired(), handlers.Me)
	auth.POST("/verify", middleware.RateLimit(10, 3600), handlers.VerifyEmail)
	auth.POST("/forgot-password", middleware.RateLimit(5, 3600), handlers.ForgotPassword)
	auth.POST("/reset-password", middleware.RateLimit(10, 3600), handlers.ResetPassword)
	auth.POST("/resend-verification", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.ResendVerification)

	// Games
	g.GET("/games", handlers.ListGames)
	g.GET("/games/:id", handlers.GetGame)
	g.GET("/featured", middleware.RateLimit(120, 3600), handlers.GetFeatured)
	g.POST("/games", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(10, 3600), bodyLimit(cfg.uploadCap), handlers.UploadGame)
	g.PUT("/games/:id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(60, 3600), handlers.UpdateGame)
	g.DELETE("/games/:id", middleware.AuthRequired(), middleware.RateLimit(10, 3600), handlers.DeleteGame)
	g.POST("/games/:id/reupload", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(10, 3600), bodyLimit(cfg.uploadCap), handlers.ReuploadGameFiles)
	g.PUT("/games/:id/visibility", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(30, 3600), handlers.ToggleVisibility)
	g.POST("/games/:id/cover", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), bodyLimit((5<<20)+(1<<20)), handlers.UpdateCoverImage)
	g.POST("/games/:id/screenshots", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), bodyLimit(cfg.uploadCap), handlers.ManageScreenshots)
	g.DELETE("/games/:id/screenshots/:index", middleware.AuthRequired(), middleware.RateLimit(60, 3600), handlers.DeleteScreenshot)

	// Reviews
	g.GET("/games/:id/reviews", handlers.ListReviews)
	g.POST("/games/:id/reviews", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), handlers.CreateReview)
	g.DELETE("/reviews/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteReview)

	// Library
	g.GET("/library", middleware.AuthRequired(), handlers.GetLibrary)
	g.POST("/library/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.AddToLibrary)
	g.DELETE("/library/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.RemoveFromLibrary)

	// Wishlist
	g.GET("/wishlist", middleware.AuthRequired(), handlers.GetWishlist)
	g.POST("/wishlist/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.AddToWishlist)
	g.DELETE("/wishlist/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.RemoveFromWishlist)

	// Profile
	g.GET("/profile/:username", handlers.GetProfile)
	g.PUT("/profile", middleware.AuthRequired(), middleware.RateLimit(10, 300), handlers.UpdateProfile)
	g.GET("/activity", middleware.AuthRequired(), handlers.GetActivity)
	g.POST("/playtime", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(60, 60), handlers.RecordPlaytime)

	// Settings
	g.DELETE("/settings/account", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.DeleteAccount)
	g.PUT("/settings/password", middleware.AuthRequired(), middleware.RateLimit(5, 3600), handlers.ChangePassword)

	// Developer pages
	g.GET("/developer/:username", handlers.GetDeveloperPage)
	g.PUT("/developer", middleware.AuthRequired(), middleware.RateLimit(10, 300), handlers.UpdateDeveloperPage)
	g.GET("/developer/:username/games", handlers.GetDeveloperGames)

	// Achievements
	g.GET("/achievements/:username", handlers.GetUserAchievements)
	g.POST("/achievements/check", middleware.AuthRequired(), middleware.RateLimit(10, 300), handlers.CheckMyAchievements)

	// Analytics
	g.POST("/games/:id/view", middleware.RateLimit(60, 60), handlers.TrackView)
	g.POST("/analytics/client", middleware.RateLimit(60, 60), handlers.TrackClientInfo)
	g.GET("/games/:id/analytics", middleware.AuthRequired(), handlers.GetGameAnalytics)

	// Notifications
	g.GET("/notifications", middleware.AuthRequired(), handlers.GetNotifications)
	g.POST("/notifications/read", middleware.AuthRequired(), handlers.MarkNotificationsRead)

	// Feed (aggregated timeline)
	g.GET("/feed", middleware.AuthRequired(), handlers.GetFeed)

	// Devlogs
	g.GET("/games/:id/devlogs", handlers.ListDevlogs)
	g.POST("/games/:id/devlogs", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), handlers.CreateDevlog)
	g.DELETE("/devlogs/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteDevlog)

	// Comments on devlogs
	g.GET("/devlogs/:id/comments", handlers.ListComments)
	g.POST("/devlogs/:id/comments", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(30, 3600), handlers.CreateComment)
	g.DELETE("/comments/:id", middleware.AuthRequired(), middleware.RateLimit(60, 3600), handlers.DeleteComment)

	// Follows
	g.POST("/follow/:username", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.FollowDeveloper)
	g.DELETE("/follow/:username", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.UnfollowDeveloper)
	g.GET("/following", middleware.AuthRequired(), handlers.GetFollowing)
	g.GET("/followers/:username", handlers.GetFollowerCount)

	// Collections / Lists
	g.GET("/collections", middleware.AuthRequired(), handlers.ListCollections)
	g.GET("/collections/public", handlers.BrowsePublicLists)
	g.GET("/collections/:id", handlers.GetCollection)
	g.POST("/collections", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.CreateCollection)
	g.PUT("/collections/:id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.UpdateCollection)
	g.DELETE("/collections/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteCollection)
	g.POST("/collections/:id/games", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.AddToCollection)
	g.DELETE("/collections/:id/games/:game_id", middleware.AuthRequired(), middleware.RateLimit(60, 300), handlers.RemoveFromCollection)

	// Admin routes — same group so AuthOptional + CSRFProtect apply.
	admin := g.Group("/admin")
	admin.Use(handlers.AdminRequired())
	admin.GET("/stats", middleware.RateLimit(120, 3600), handlers.AdminStats)
	admin.GET("/users", middleware.RateLimit(120, 3600), handlers.AdminListUsers)
	admin.DELETE("/users/:id", middleware.RateLimit(10, 3600), handlers.AdminDeleteUser)
	admin.GET("/games", middleware.RateLimit(120, 3600), handlers.AdminListGames)
	admin.DELETE("/games/:id", middleware.RateLimit(10, 3600), handlers.AdminDeleteGame)
	admin.PUT("/games/:id/publish", middleware.RateLimit(30, 3600), handlers.AdminTogglePublish)
	admin.GET("/featured", middleware.RateLimit(120, 3600), handlers.AdminGetFeatured)
	admin.PUT("/featured", middleware.RateLimit(60, 3600), handlers.AdminSetFeatured)
	admin.GET("/analytics", middleware.RateLimit(120, 3600), handlers.AdminSiteAnalytics)

	// Image uploads
	g.POST("/upload/image", middleware.AuthRequired(), middleware.RateLimit(20, 3600), bodyLimit(cfg.imageCap), handlers.UploadImage)

	// Chunked upload pipeline — see docs/superpowers/specs/2026-05-21-chunked-upload-design.md
	g.POST("/uploads/init", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), bodyLimit(1<<20), handlers.InitUpload)
	g.PUT("/uploads/:upload_id/chunks", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(2000, 3600), bodyLimit(cfg.chunkPutCap), handlers.PutChunk)
	g.GET("/uploads/:upload_id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(600, 3600), handlers.GetUploadStatus)
	g.POST("/uploads/:upload_id/finalize", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20, 3600), bodyLimit(1<<20), handlers.FinalizeUpload)
	g.DELETE("/uploads/:upload_id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(60, 3600), handlers.CancelUpload)

	// Seed demo data (admin only)
	g.POST("/seed", middleware.AuthRequired(), middleware.RateLimit(3, 3600), handlers.SeedData)

	// API Keys
	g.GET("/api-keys", middleware.AuthRequired(), handlers.ListAPIKeysHandler)
	g.POST("/api-keys", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(10, 3600), handlers.CreateAPIKeyHandler)
	g.DELETE("/api-keys/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteAPIKeyHandler)

	// Webhooks — outbound event subscriptions
	g.POST("/webhooks", middleware.AuthRequired(), middleware.RateLimit(20, 3600), handlers.CreateWebhookHandler)
	g.GET("/webhooks", middleware.AuthRequired(), handlers.ListWebhooksHandler)
	g.GET("/webhooks/:id", middleware.AuthRequired(), handlers.GetWebhookHandler)
	g.PUT("/webhooks/:id", middleware.AuthRequired(), middleware.RateLimit(20, 3600), handlers.UpdateWebhookHandler)
	g.DELETE("/webhooks/:id", middleware.AuthRequired(), middleware.RateLimit(10, 3600), handlers.DeleteWebhookHandler)
	g.GET("/webhooks/:id/deliveries", middleware.AuthRequired(), handlers.ListWebhookDeliveriesHandler)

	// Build channels — per-game internal/beta/stable builds
	g.GET("/games/:id/builds", middleware.AuthRequired(), handlers.ListBuildsHandler)
	g.GET("/games/:id/builds/:build_id", middleware.AuthRequired(), handlers.GetBuildHandler)
	g.PUT("/games/:id/builds/:build_id/activate", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(30, 3600), handlers.ActivateBuildHandler)
	g.POST("/games/:id/builds/:build_id/rollback", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(30, 3600), handlers.RollbackBuildHandler)
	g.DELETE("/games/:id/builds/:build_id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(30, 3600), handlers.DeleteBuildHandler)

	// Multiplayer substrate — game-scoped SDK keys and runtime session tokens.
	// SDK keys are long-lived per-game credentials for server-side logic.
	// Session tokens are short-lived (5 min) tokens minted by the SPA and
	// passed into the game iframe for game-scoped API calls + WebSocket auth.
	g.GET("/games/:id/sdk-keys", middleware.AuthRequired(), middleware.RateLimit(60, 3600), handlers.ListGameAPIKeysHandler)
	g.POST("/games/:id/sdk-keys", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(10, 3600), handlers.CreateGameAPIKeyHandler)
	g.DELETE("/games/:id/sdk-keys/:kid", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.DeleteGameAPIKeyHandler)
	g.POST("/games/:id/sdk-token", middleware.AuthRequired(), middleware.RateLimit(60, 3600), handlers.MintGameSessionTokenHandler)
	g.DELETE("/sdk-tokens/:id", middleware.AuthRequired(), middleware.RateLimit(30, 3600), handlers.RevokeGameSessionTokenHandler)

	// Play sessions — track live game sessions for analytics + realtime
	// player counts. Accept session auth (SPA) or pm_gs_ tokens (game
	// iframe can call directly via CORS). Heartbeat is rate-limited to
	// 12/min (one every 5s; SPA sends every 30s).
	g.POST("/games/:id/play-sessions", middleware.AuthRequiredOrGameSession(), middleware.RateLimit(30, 60), handlers.OpenPlaySessionHandler)
	g.POST("/play-sessions/:sid/heartbeat", middleware.AuthRequiredOrGameSession(), middleware.RateLimit(12, 60), handlers.HeartbeatPlaySessionHandler)
	g.POST("/play-sessions/:sid/end", middleware.AuthRequiredOrGameSession(), middleware.RateLimit(10, 60), handlers.EndPlaySessionHandler)

	// Public lobby browser — list open, public, non-started lobbies.
	g.GET("/games/:id/lobbies", middleware.RateLimit(30, 60), handlers.ListPublicLobbiesHandler)
}

// NewTestConfig returns the body-cap configuration used by
// testutil.NewTestServer. The chunk PUT cap matches the
// production default (8 MiB chunk + 1 MiB headroom) so tests
// can exercise the full chunk size; the other caps are
// 1 MiB — small enough to keep rate-limit math sane.
func NewTestConfig() TestConfig {
	return TestConfig{
		uploadCap:   1 << 20,
		imageCap:    1 << 20,
		chunkPutCap: (8 << 20) + (1 << 20),
	}
}

// TestConfig is the public form of apiConfig so test code in
// other packages can construct a router without depending on the
// private type.
type TestConfig = apiConfig

// MountAPIRoutesForTest exposes the package-private
// mountAPIRoutes for testutil. The production code path uses
// New(); tests that don't need the embed.FS / SPA fallback use
// this entry point to mount a minimal router.
func MountAPIRoutesForTest(r *gin.Engine, cfg TestConfig) {
	mountAPIRoutes(r.Group("/api/v1"), cfg)
}
