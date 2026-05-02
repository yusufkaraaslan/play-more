package handlers

import (
	"crypto/sha256"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// TrackView records a game page view.
func TrackView(c *gin.Context) {
	gameID := c.Param("id")
	userID := ""
	user := middleware.GetUser(c)
	if user != nil {
		userID = user.ID
	}

	// Hash IP for privacy using the per-server-start random salt.
	ip := middleware.RealClientIP(c)
	ipHash := fmt.Sprintf("%x", sha256.Sum256([]byte(ip+middleware.AnalyticsSalt())))[:16]

	referrer := c.Query("ref")

	storage.DB.Exec(
		`INSERT INTO game_views (game_id, user_id, ip_hash, referrer) VALUES (?, ?, ?, ?)`,
		gameID, userID, ipHash, referrer,
	)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// TrackClientInfo records screen resolution and WebGPU capability.
func TrackClientInfo(c *gin.Context) {
	var input struct {
		ScreenRes string `json:"screen_res"`
		HasWebGPU bool   `json:"has_webgpu"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"})
		return
	}

	ip := middleware.RealClientIP(c)
	ipHash := fmt.Sprintf("%x", sha256.Sum256([]byte(ip+middleware.AnalyticsSalt())))[:16]

	webgpu := 0
	if input.HasWebGPU {
		webgpu = 1
	}

	// Update the most recent page_view for this IP with client info
	storage.DB.Exec(
		`UPDATE page_views SET screen_res = ?, has_webgpu = ? WHERE id = (SELECT id FROM page_views WHERE ip_hash = ? ORDER BY created_at DESC LIMIT 1)`,
		input.ScreenRes, webgpu, ipHash,
	)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type AnalyticsData struct {
	TotalViews   int            `json:"total_views"`
	UniqueViews  int            `json:"unique_views"`
	TotalPlays   int            `json:"total_plays"`
	ReviewCount  int            `json:"review_count"`
	LibraryAdds  int            `json:"library_adds"`
	WishlistAdds int            `json:"wishlist_adds"`
	ViewsByDay   []DayStat      `json:"views_by_day"`
	TopReferrers []ReferrerStat `json:"top_referrers"`
}

type DayStat struct {
	Date  string `json:"date"`
	Views int    `json:"views"`
}

type ReferrerStat struct {
	Referrer string `json:"referrer"`
	Count    int    `json:"count"`
}

// GetGameAnalytics returns analytics for a game (developer only).
func GetGameAnalytics(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	gameID := c.Param("id")

	// Verify ownership
	var devID string
	err := storage.DB.QueryRow(`SELECT developer_id FROM games WHERE id = ?`, gameID).Scan(&devID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if devID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}

	data := AnalyticsData{}

	// Total views — COUNT DISTINCT prevents a single IP from inflating the count
	// by refreshing the page repeatedly.
	storage.DB.QueryRow(`SELECT COUNT(DISTINCT ip_hash) FROM game_views WHERE game_id = ?`, gameID).Scan(&data.TotalViews)

	// Unique views (by ip_hash) — same as total since we already deduplicate.
	storage.DB.QueryRow(`SELECT COUNT(DISTINCT ip_hash) FROM game_views WHERE game_id = ?`, gameID).Scan(&data.UniqueViews)

	// Total plays — DISTINCT prevents the same user from inflating via heartbeat spam.
	storage.DB.QueryRow(`SELECT COALESCE(SUM(play_count), 0) FROM playtime WHERE game_id = ?`, gameID).Scan(&data.TotalPlays)

	// Review count
	storage.DB.QueryRow(`SELECT COUNT(*) FROM reviews WHERE game_id = ?`, gameID).Scan(&data.ReviewCount)

	// Library adds
	storage.DB.QueryRow(`SELECT COUNT(*) FROM library WHERE game_id = ?`, gameID).Scan(&data.LibraryAdds)

	// Wishlist adds
	storage.DB.QueryRow(`SELECT COUNT(*) FROM wishlist WHERE game_id = ?`, gameID).Scan(&data.WishlistAdds)

	// Views by day (last 30 days) — deduplicate per IP per day so one user can't
	// pad a single day's stats by refreshing.
	rows, err := storage.DB.Query(
		`SELECT DATE(created_at) as day, COUNT(DISTINCT ip_hash) as cnt
		 FROM game_views WHERE game_id = ? AND created_at >= datetime('now', '-30 days')
		 GROUP BY day ORDER BY day ASC`, gameID,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d DayStat
			rows.Scan(&d.Date, &d.Views)
			data.ViewsByDay = append(data.ViewsByDay, d)
		}
	}
	if data.ViewsByDay == nil {
		data.ViewsByDay = []DayStat{}
	}

	// Top referrers
	rows2, err := storage.DB.Query(
		`SELECT referrer, COUNT(*) as cnt FROM game_views
		 WHERE game_id = ? AND referrer != '' GROUP BY referrer ORDER BY cnt DESC LIMIT 10`, gameID,
	)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var r ReferrerStat
			rows2.Scan(&r.Referrer, &r.Count)
			data.TopReferrers = append(data.TopReferrers, r)
		}
	}
	if data.TopReferrers == nil {
		data.TopReferrers = []ReferrerStat{}
	}

	c.JSON(http.StatusOK, gin.H{"analytics": data})
}
