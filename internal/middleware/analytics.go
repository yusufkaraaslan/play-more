package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type pageView struct {
	Path       string
	Method     string
	IPHash     string
	UserAgent  string
	Referrer   string
	UserID     string
	StatusCode int
	ResponseMs int64
	CreatedAt  time.Time
}

var viewChan = make(chan pageView, 1000)
var analyticsCancel context.CancelFunc

// TrackPageView logs every request to the page_views table asynchronously.
func TrackPageView() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Skip static assets and game files
		if strings.HasPrefix(path, "/assets/") || strings.HasPrefix(path, "/uploads/") {
			c.Next()
			return
		}
		if strings.HasPrefix(path, "/play/") && strings.Count(path, "/") > 2 {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		elapsed := time.Since(start).Milliseconds()

		// Hash IP
		ip := c.ClientIP()
		ipHash := fmt.Sprintf("%x", sha256.Sum256([]byte(ip+"playmore-salt")))[:16]

		// Truncate user agent
		ua := c.Request.UserAgent()
		if len(ua) > 200 {
			ua = ua[:200]
		}

		// Get user ID from context only — no DB fallback (fix #8)
		userID := ""
		if val, exists := c.Get(UserKey); exists {
			if user, ok := val.(*models.User); ok {
				userID = user.ID
			}
		}

		// Referrer
		ref := c.Request.Referer()
		if len(ref) > 500 {
			ref = ref[:500]
		}

		// Send to async writer (non-blocking)
		select {
		case viewChan <- pageView{
			Path:       path,
			Method:     c.Request.Method,
			IPHash:     ipHash,
			UserAgent:  ua,
			Referrer:   ref,
			UserID:     userID,
			StatusCode: c.Writer.Status(),
			ResponseMs: elapsed,
			CreatedAt:  time.Now(),
		}:
		default:
			// Channel full, drop the view
		}
	}
}

// StartAnalyticsWriter starts the background goroutine that batch-writes
// page views to the database and cleans up old data.
func StartAnalyticsWriter() {
	ctx, cancel := context.WithCancel(context.Background())
	analyticsCancel = cancel

	// Batch writer
	go func() {
		batch := make([]pageView, 0, 50)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case pv := <-viewChan:
				batch = append(batch, pv)
				if len(batch) >= 50 {
					flushBatch(batch)
					batch = batch[:0]
				}
			case <-ticker.C:
				if len(batch) > 0 {
					flushBatch(batch)
					batch = batch[:0]
				}
			case <-ctx.Done():
				// Flush remaining on shutdown
				if len(batch) > 0 {
					flushBatch(batch)
				}
				return
			}
		}
	}()

	// Cleanup: delete page_views older than 90 days
	go func() {
		for {
			select {
			case <-time.After(24 * time.Hour):
				storage.DB.Exec(`DELETE FROM page_views WHERE created_at < datetime('now', '-90 days')`)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopAnalyticsWriter flushes pending data and stops goroutines.
func StopAnalyticsWriter() {
	if analyticsCancel != nil {
		analyticsCancel()
	}
}

func flushBatch(batch []pageView) {
	tx, err := storage.DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback() // no-op after successful commit

	stmt, err := tx.Prepare(`INSERT INTO page_views (path, method, ip_hash, user_agent, referrer, user_id, status_code, response_ms, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return
	}
	defer stmt.Close()

	for _, pv := range batch {
		if _, err := stmt.Exec(pv.Path, pv.Method, pv.IPHash, pv.UserAgent, pv.Referrer, pv.UserID, pv.StatusCode, pv.ResponseMs, pv.CreatedAt); err != nil {
			log.Printf("analytics insert error: %v", err)
		}
	}
	tx.Commit()
}
