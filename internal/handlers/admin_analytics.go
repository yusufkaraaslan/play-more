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

	// Browsers (last 30 days) - grouped in SQL to avoid loading all UAs
	rows3, err := storage.DB.Query(
		`SELECT
			CASE
				WHEN LOWER(user_agent) LIKE '%firefox%' THEN 'Firefox'
				WHEN LOWER(user_agent) LIKE '%edg/%' OR LOWER(user_agent) LIKE '%edge/%' THEN 'Edge'
				WHEN LOWER(user_agent) LIKE '%opr/%' OR LOWER(user_agent) LIKE '%opera%' THEN 'Opera'
				WHEN LOWER(user_agent) LIKE '%chrome%' OR LOWER(user_agent) LIKE '%chromium%' THEN 'Chrome'
				WHEN LOWER(user_agent) LIKE '%safari%' THEN 'Safari'
				WHEN LOWER(user_agent) LIKE '%curl%' THEN 'curl'
				WHEN LOWER(user_agent) LIKE '%bot%' OR LOWER(user_agent) LIKE '%crawl%' THEN 'Bot'
				ELSE 'Other'
			END as browser,
			COUNT(*) as cnt
		 FROM page_views
		 WHERE created_at >= datetime('now', '-30 days') AND user_agent != ''
		 GROUP BY browser ORDER BY cnt DESC`,
	)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var b BrowserStat
			rows3.Scan(&b.Name, &b.Count)
			data.Browsers = append(data.Browsers, b)
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

