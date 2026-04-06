package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type PeriodStats struct {
	Views  int `json:"views"`
	Unique int `json:"unique"`
}

type PathStat struct {
	Path  string `json:"path"`
	Views int    `json:"views"`
}

type HourlyStat struct {
	Hour  string `json:"hour"`
	Views int    `json:"views"`
}

type BrowserStat struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type RefStat struct {
	Referrer string `json:"referrer"`
	Count    int    `json:"count"`
}

type SiteAnalytics struct {
	Today        PeriodStats   `json:"today"`
	Week         PeriodStats   `json:"week"`
	Month        PeriodStats   `json:"month"`
	PopularPages []PathStat    `json:"popular_pages"`
	Hourly       []HourlyStat  `json:"hourly"`
	Browsers     []BrowserStat `json:"browsers"`
	Referrers    []RefStat     `json:"referrers"`
	ErrorRate    float64       `json:"error_rate"`
	AvgResponse  int           `json:"avg_response_ms"`
}

func AdminSiteAnalytics(c *gin.Context) {
	data := SiteAnalytics{}

	// Today
	storage.DB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT ip_hash) FROM page_views WHERE created_at >= datetime('now', 'start of day')`).Scan(&data.Today.Views, &data.Today.Unique)
	// This week
	storage.DB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT ip_hash) FROM page_views WHERE created_at >= datetime('now', '-7 days')`).Scan(&data.Week.Views, &data.Week.Unique)
	// This month
	storage.DB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT ip_hash) FROM page_views WHERE created_at >= datetime('now', '-30 days')`).Scan(&data.Month.Views, &data.Month.Unique)

	// Popular pages (last 7 days, top 15)
	rows1, err := storage.DB.Query(
		`SELECT path, COUNT(*) as cnt FROM page_views
		 WHERE created_at >= datetime('now', '-7 days')
		 GROUP BY path ORDER BY cnt DESC LIMIT 15`,
	)
	if err == nil {
		defer rows1.Close()
		for rows1.Next() {
			var p PathStat
			rows1.Scan(&p.Path, &p.Views)
			data.PopularPages = append(data.PopularPages, p)
		}
	}
	if data.PopularPages == nil {
		data.PopularPages = []PathStat{}
	}

	// Hourly (last 24 hours)
	rows2, err := storage.DB.Query(
		`SELECT strftime('%Y-%m-%d %H:00', created_at) as hour, COUNT(*) as cnt
		 FROM page_views WHERE created_at >= datetime('now', '-24 hours')
		 GROUP BY hour ORDER BY hour ASC`,
	)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var h HourlyStat
			rows2.Scan(&h.Hour, &h.Views)
			data.Hourly = append(data.Hourly, h)
		}
	}
	if data.Hourly == nil {
		data.Hourly = []HourlyStat{}
	}

	// Browsers (last 30 days) - parsed from user_agent
	rows3, err := storage.DB.Query(
		`SELECT user_agent, COUNT(*) as cnt FROM page_views
		 WHERE created_at >= datetime('now', '-30 days') AND user_agent != ''
		 GROUP BY user_agent`,
	)
	if err == nil {
		defer rows3.Close()
		browserCounts := map[string]int{}
		for rows3.Next() {
			var ua string
			var cnt int
			rows3.Scan(&ua, &cnt)
			browser := parseBrowser(ua)
			browserCounts[browser] += cnt
		}
		for name, count := range browserCounts {
			data.Browsers = append(data.Browsers, BrowserStat{Name: name, Count: count})
		}
	}
	if data.Browsers == nil {
		data.Browsers = []BrowserStat{}
	}

	// Referrers (last 30 days, external only)
	rows4, err := storage.DB.Query(
		`SELECT referrer, COUNT(*) as cnt FROM page_views
		 WHERE created_at >= datetime('now', '-30 days') AND referrer != '' AND referrer NOT LIKE '%localhost%'
		 GROUP BY referrer ORDER BY cnt DESC LIMIT 10`,
	)
	if err == nil {
		defer rows4.Close()
		for rows4.Next() {
			var r RefStat
			rows4.Scan(&r.Referrer, &r.Count)
			data.Referrers = append(data.Referrers, r)
		}
	}
	if data.Referrers == nil {
		data.Referrers = []RefStat{}
	}

	// Error rate (last 24h)
	var total, errors int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM page_views WHERE created_at >= datetime('now', '-24 hours')`).Scan(&total)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM page_views WHERE created_at >= datetime('now', '-24 hours') AND status_code >= 400`).Scan(&errors)
	if total > 0 {
		data.ErrorRate = float64(errors) / float64(total) * 100
	}

	// Average response time (last 24h)
	storage.DB.QueryRow(`SELECT COALESCE(AVG(response_ms), 0) FROM page_views WHERE created_at >= datetime('now', '-24 hours')`).Scan(&data.AvgResponse)

	c.JSON(http.StatusOK, gin.H{"analytics": data})
}

func parseBrowser(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "edg/") || strings.Contains(ua, "edge/"):
		return "Edge"
	case strings.Contains(ua, "opr/") || strings.Contains(ua, "opera"):
		return "Opera"
	case strings.Contains(ua, "chrome") || strings.Contains(ua, "chromium"):
		return "Chrome"
	case strings.Contains(ua, "firefox") || strings.Contains(ua, "gecko/"):
		return "Firefox"
	case strings.Contains(ua, "safari") && !strings.Contains(ua, "chrome"):
		return "Safari"
	case strings.Contains(ua, "curl"):
		return "curl"
	case strings.Contains(ua, "bot") || strings.Contains(ua, "crawl"):
		return "Bot"
	default:
		return "Other"
	}
}
