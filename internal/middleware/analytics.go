package middleware

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// analyticsSalt is generated once per server start. IP hashes computed with
// it cannot be correlated across restarts (or reverse-engineered with a
// known plaintext like a corporate IP).
var analyticsSalt = func() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate analytics salt: %v", err))
	}
	return hex.EncodeToString(b)
}()

// AnalyticsSalt returns the per-server-start random salt used for IP hashing
// in analytics. Exported so handlers/analytics.go can use the same salt.
func AnalyticsSalt() string { return analyticsSalt }

type pageView struct {
	Path       string
	Method     string
	IPHash     string
	UserAgent  string
	Referrer   string
	UserID     string
	StatusCode int
	ResponseMs int64
	DeviceType string
	OS         string
	SessionID  string
	CreatedAt  time.Time
}

var viewChan = make(chan pageView, 1000)
var analyticsCancel context.CancelFunc

// Session tracking: ip_hash → (session_id, last_seen)
var sessions = struct {
	sync.Mutex
	m map[string]sessionEntry
}{m: make(map[string]sessionEntry)}

type sessionEntry struct {
	ID       string
	LastSeen time.Time
}

const sessionTimeout = 30 * time.Minute

func getSessionID(ipHash string) string {
	sessions.Lock()
	defer sessions.Unlock()
	now := time.Now()
	if s, ok := sessions.m[ipHash]; ok && now.Sub(s.LastSeen) < sessionTimeout {
		s.LastSeen = now
		sessions.m[ipHash] = s
		return s.ID
	}
	// New session
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(ipHash+now.String())))[:12]
	sessions.m[ipHash] = sessionEntry{ID: id, LastSeen: now}
	return id
}

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

		ip := RealClientIP(c)
		ipHash := fmt.Sprintf("%x", sha256.Sum256([]byte(ip+analyticsSalt)))[:16]

		ua := c.Request.UserAgent()
		if len(ua) > 200 {
			ua = ua[:200]
		}

		userID := ""
		if val, exists := c.Get(UserKey); exists {
			if user, ok := val.(*models.User); ok {
				userID = user.ID
			}
		}

		ref := c.Request.Referer()
		if len(ref) > 500 {
			ref = ref[:500]
		}

		deviceType, osName := parseUA(ua)
		sessionID := getSessionID(ipHash)

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
			DeviceType: deviceType,
			OS:         osName,
			SessionID:  sessionID,
			CreatedAt:  time.Now(),
		}:
		default:
		}
	}
}

func parseUA(ua string) (deviceType, osName string) {
	lower := strings.ToLower(ua)

	// Device type
	switch {
	case strings.Contains(lower, "mobile") || strings.Contains(lower, "android") && !strings.Contains(lower, "tablet"):
		deviceType = "Mobile"
	case strings.Contains(lower, "tablet") || strings.Contains(lower, "ipad"):
		deviceType = "Tablet"
	default:
		deviceType = "Desktop"
	}

	// OS
	switch {
	case strings.Contains(lower, "windows"):
		osName = "Windows"
	case strings.Contains(lower, "mac os") || strings.Contains(lower, "macos"):
		osName = "macOS"
	case strings.Contains(lower, "android"):
		osName = "Android"
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad") || strings.Contains(lower, "ios"):
		osName = "iOS"
	case strings.Contains(lower, "linux"):
		osName = "Linux"
	case strings.Contains(lower, "chromeos") || strings.Contains(lower, "cros"):
		osName = "ChromeOS"
	default:
		osName = "Other"
	}
	return
}

// StartAnalyticsWriter starts background goroutines for batch writing and cleanup.
func StartAnalyticsWriter() {
	ctx, cancel := context.WithCancel(context.Background())
	analyticsCancel = cancel

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
				if len(batch) > 0 {
					flushBatch(batch)
				}
				return
			}
		}
	}()

	// Cleanup old data + expired sessions
	go func() {
		for {
			select {
			case <-time.After(1 * time.Hour):
				storage.DB.Exec(`DELETE FROM page_views WHERE created_at < datetime('now', '-90 days')`)
				storage.DB.Exec(`DELETE FROM game_views WHERE created_at < datetime('now', '-365 days')`)
				// Clean expired sessions from memory
				sessions.Lock()
				now := time.Now()
				for k, v := range sessions.m {
					if now.Sub(v.LastSeen) > sessionTimeout*2 {
						delete(sessions.m, k)
					}
				}
				sessions.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
}

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
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO page_views (path, method, ip_hash, user_agent, referrer, user_id, status_code, response_ms, device_type, os, session_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return
	}
	defer stmt.Close()

	for _, pv := range batch {
		if _, err := stmt.Exec(pv.Path, pv.Method, pv.IPHash, pv.UserAgent, pv.Referrer, pv.UserID, pv.StatusCode, pv.ResponseMs, pv.DeviceType, pv.OS, pv.SessionID, pv.CreatedAt); err != nil {
			log.Printf("analytics insert error: %v", err)
		}
	}
	tx.Commit()
}
