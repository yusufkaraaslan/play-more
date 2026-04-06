package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type PeriodStats struct {
	Views  int `json:"views"`
	Unique int `json:"unique"`
}

type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type HourlyStat struct {
	Hour  string `json:"hour"`
	Views int    `json:"views"`
}

type DayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type SiteAnalytics struct {
	Today          PeriodStats `json:"today"`
	Week           PeriodStats `json:"week"`
	Month          PeriodStats `json:"month"`
	LastWeek       PeriodStats `json:"last_week"`
	RealTimeActive int         `json:"realtime_active"`
	PopularPages   []NameCount `json:"popular_pages"`
	Hourly         []HourlyStat `json:"hourly"`
	Browsers       []NameCount `json:"browsers"`
	Devices        []NameCount `json:"devices"`
	OSes           []NameCount `json:"oses"`
	Referrers      []NameCount `json:"referrers"`
	ErrorRate      float64     `json:"error_rate"`
	AvgResponse    int         `json:"avg_response_ms"`
	BounceRate     float64     `json:"bounce_rate"`
	AvgPagesPerSession float64 `json:"avg_pages_per_session"`
	AvgSessionDuration float64 `json:"avg_session_duration_sec"`
	NewVsReturning []NameCount `json:"new_vs_returning"`
	WebGPUSupport  []NameCount `json:"webgpu_support"`
	// User stats
	TotalUsers      int        `json:"total_users"`
	NewUsersToday   int        `json:"new_users_today"`
	NewUsersWeek    int        `json:"new_users_week"`
	ActiveUsersDay  int        `json:"active_users_day"`
	ActiveUsersWeek int        `json:"active_users_week"`
	RegistrationsPerDay []DayCount `json:"registrations_per_day"`
	// Gameplay stats
	TotalPlaySessions int     `json:"total_play_sessions"`
	AvgPlayDuration   float64 `json:"avg_play_duration_sec"`
	MostPlayedGames   []NameCount `json:"most_played_games"`
}

func AdminSiteAnalytics(c *gin.Context) {
	data := SiteAnalytics{}

	// Period stats
	storage.DB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT ip_hash) FROM page_views WHERE created_at >= datetime('now', 'start of day')`).Scan(&data.Today.Views, &data.Today.Unique)
	storage.DB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT ip_hash) FROM page_views WHERE created_at >= datetime('now', '-7 days')`).Scan(&data.Week.Views, &data.Week.Unique)
	storage.DB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT ip_hash) FROM page_views WHERE created_at >= datetime('now', '-30 days')`).Scan(&data.Month.Views, &data.Month.Unique)
	storage.DB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT ip_hash) FROM page_views WHERE created_at >= datetime('now', '-14 days') AND created_at < datetime('now', '-7 days')`).Scan(&data.LastWeek.Views, &data.LastWeek.Unique)

	// Real-time: active in last 5 minutes
	storage.DB.QueryRow(`SELECT COUNT(DISTINCT ip_hash) FROM page_views WHERE created_at >= datetime('now', '-5 minutes')`).Scan(&data.RealTimeActive)

	// Popular pages
	data.PopularPages = queryNameCounts(`SELECT path, COUNT(*) as cnt FROM page_views WHERE created_at >= datetime('now', '-7 days') GROUP BY path ORDER BY cnt DESC LIMIT 15`)

	// Hourly
	rows2, err := storage.DB.Query(`SELECT strftime('%Y-%m-%d %H:00', created_at) as hour, COUNT(*) as cnt FROM page_views WHERE created_at >= datetime('now', '-24 hours') GROUP BY hour ORDER BY hour ASC`)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var h HourlyStat
			rows2.Scan(&h.Hour, &h.Views)
			data.Hourly = append(data.Hourly, h)
		}
	}
	if data.Hourly == nil { data.Hourly = []HourlyStat{} }

	// Browsers
	data.Browsers = queryNameCounts(`SELECT CASE WHEN LOWER(user_agent) LIKE '%firefox%' THEN 'Firefox' WHEN LOWER(user_agent) LIKE '%edg/%' OR LOWER(user_agent) LIKE '%edge/%' THEN 'Edge' WHEN LOWER(user_agent) LIKE '%opr/%' OR LOWER(user_agent) LIKE '%opera%' THEN 'Opera' WHEN LOWER(user_agent) LIKE '%chrome%' OR LOWER(user_agent) LIKE '%chromium%' THEN 'Chrome' WHEN LOWER(user_agent) LIKE '%safari%' THEN 'Safari' WHEN LOWER(user_agent) LIKE '%curl%' THEN 'curl' WHEN LOWER(user_agent) LIKE '%bot%' OR LOWER(user_agent) LIKE '%crawl%' THEN 'Bot' ELSE 'Other' END as browser, COUNT(*) as cnt FROM page_views WHERE created_at >= datetime('now', '-30 days') AND user_agent != '' GROUP BY browser ORDER BY cnt DESC`)

	// Devices
	data.Devices = queryNameCounts(`SELECT CASE WHEN device_type = '' THEN 'Unknown' ELSE device_type END, COUNT(*) FROM page_views WHERE created_at >= datetime('now', '-30 days') GROUP BY device_type ORDER BY COUNT(*) DESC`)

	// OS
	data.OSes = queryNameCounts(`SELECT CASE WHEN os = '' THEN 'Unknown' ELSE os END, COUNT(*) FROM page_views WHERE created_at >= datetime('now', '-30 days') GROUP BY os ORDER BY COUNT(*) DESC`)

	// Referrers
	data.Referrers = queryNameCounts(`SELECT referrer, COUNT(*) as cnt FROM page_views WHERE created_at >= datetime('now', '-30 days') AND referrer != '' AND referrer NOT LIKE '%localhost%' GROUP BY referrer ORDER BY cnt DESC LIMIT 10`)

	// Error rate + avg response (24h)
	var total, errors int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM page_views WHERE created_at >= datetime('now', '-24 hours')`).Scan(&total)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM page_views WHERE created_at >= datetime('now', '-24 hours') AND status_code >= 400`).Scan(&errors)
	if total > 0 { data.ErrorRate = float64(errors) / float64(total) * 100 }
	storage.DB.QueryRow(`SELECT COALESCE(AVG(response_ms), 0) FROM page_views WHERE created_at >= datetime('now', '-24 hours')`).Scan(&data.AvgResponse)

	// Session metrics (last 7 days)
	var totalSessions, bounceSessions int
	var totalPages int
	var totalDuration float64
	storage.DB.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM page_views WHERE created_at >= datetime('now', '-7 days') AND session_id != ''`).Scan(&totalSessions)
	// Bounce = sessions with only 1 page view
	storage.DB.QueryRow(`SELECT COUNT(*) FROM (SELECT session_id, COUNT(*) as c FROM page_views WHERE created_at >= datetime('now', '-7 days') AND session_id != '' GROUP BY session_id HAVING c = 1)`).Scan(&bounceSessions)
	if totalSessions > 0 { data.BounceRate = float64(bounceSessions) / float64(totalSessions) * 100 }
	// Pages per session
	storage.DB.QueryRow(`SELECT COUNT(*) FROM page_views WHERE created_at >= datetime('now', '-7 days') AND session_id != ''`).Scan(&totalPages)
	if totalSessions > 0 { data.AvgPagesPerSession = float64(totalPages) / float64(totalSessions) }
	// Avg session duration
	storage.DB.QueryRow(`SELECT COALESCE(AVG(duration), 0) FROM (SELECT session_id, (julianday(MAX(created_at)) - julianday(MIN(created_at))) * 86400 as duration FROM page_views WHERE created_at >= datetime('now', '-7 days') AND session_id != '' GROUP BY session_id HAVING COUNT(*) > 1)`).Scan(&totalDuration)
	data.AvgSessionDuration = totalDuration

	// New vs returning (last 30 days)
	var newVisitors, returningVisitors int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM (SELECT ip_hash, MIN(created_at) as first_seen FROM page_views GROUP BY ip_hash HAVING first_seen >= datetime('now', '-30 days'))`).Scan(&newVisitors)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM (SELECT ip_hash, MIN(created_at) as first_seen FROM page_views GROUP BY ip_hash HAVING first_seen < datetime('now', '-30 days'))`).Scan(&returningVisitors)
	data.NewVsReturning = []NameCount{{"New", newVisitors}, {"Returning", returningVisitors}}

	// WebGPU support
	var webgpuYes, webgpuNo int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM page_views WHERE has_webgpu = 1 AND created_at >= datetime('now', '-30 days')`).Scan(&webgpuYes)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM page_views WHERE has_webgpu = 0 AND created_at >= datetime('now', '-30 days')`).Scan(&webgpuNo)
	data.WebGPUSupport = []NameCount{{"Supported", webgpuYes}, {"Not Supported", webgpuNo}}

	// User stats
	storage.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&data.TotalUsers)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE created_at >= datetime('now', 'start of day')`).Scan(&data.NewUsersToday)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE created_at >= datetime('now', '-7 days')`).Scan(&data.NewUsersWeek)
	storage.DB.QueryRow(`SELECT COUNT(DISTINCT user_id) FROM page_views WHERE created_at >= datetime('now', '-24 hours') AND user_id != ''`).Scan(&data.ActiveUsersDay)
	storage.DB.QueryRow(`SELECT COUNT(DISTINCT user_id) FROM page_views WHERE created_at >= datetime('now', '-7 days') AND user_id != ''`).Scan(&data.ActiveUsersWeek)

	// Registrations per day (last 14 days)
	rows6, err := storage.DB.Query(`SELECT DATE(created_at), COUNT(*) FROM users WHERE created_at >= datetime('now', '-14 days') GROUP BY DATE(created_at) ORDER BY DATE(created_at) ASC`)
	if err == nil {
		defer rows6.Close()
		for rows6.Next() {
			var d DayCount
			rows6.Scan(&d.Date, &d.Count)
			data.RegistrationsPerDay = append(data.RegistrationsPerDay, d)
		}
	}
	if data.RegistrationsPerDay == nil { data.RegistrationsPerDay = []DayCount{} }

	// Gameplay stats
	storage.DB.QueryRow(`SELECT COALESCE(SUM(play_count), 0) FROM playtime`).Scan(&data.TotalPlaySessions)
	storage.DB.QueryRow(`SELECT COALESCE(AVG(total_seconds / NULLIF(play_count, 0)), 0) FROM playtime WHERE play_count > 0`).Scan(&data.AvgPlayDuration)

	// Most played games
	data.MostPlayedGames = queryNameCounts(`SELECT g.title, COALESCE(SUM(p.play_count), 0) as plays FROM playtime p JOIN games g ON p.game_id = g.id GROUP BY g.id ORDER BY plays DESC LIMIT 10`)

	c.JSON(http.StatusOK, gin.H{"analytics": data})
}

func queryNameCounts(query string) []NameCount {
	rows, err := storage.DB.Query(query)
	if err != nil {
		return []NameCount{}
	}
	defer rows.Close()
	var result []NameCount
	for rows.Next() {
		var nc NameCount
		rows.Scan(&nc.Name, &nc.Count)
		result = append(result, nc)
	}
	if result == nil { result = []NameCount{} }
	return result
}
