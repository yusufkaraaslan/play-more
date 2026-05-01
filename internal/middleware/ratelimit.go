package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// maxLimiterKeys caps the total number of tracked keys to bound memory under
// IP-rotation attacks. When exceeded the map is dropped wholesale (next
// requests start with a clean slate — better than running OOM).
const maxLimiterKeys = 100_000

type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
}

var limiter = &rateLimiter{requests: make(map[string][]time.Time)}

// allowKey records a hit for key and returns false if the per-window quota
// is exceeded. Used by both the IP-keyed middleware and handler-level
// per-account/per-email guards.
func allowKey(key string, maxRequests int, window time.Duration) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if len(limiter.requests) > maxLimiterKeys {
		limiter.requests = make(map[string][]time.Time)
	}

	now := time.Now()
	cutoff := now.Add(-window)
	times := limiter.requests[key]
	valid := make([]time.Time, 0, len(times))
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= maxRequests {
		limiter.requests[key] = valid
		return false
	}
	valid = append(valid, now)
	limiter.requests[key] = valid
	return true
}

// RateLimit returns middleware that limits requests per IP per window.
// maxRequests per windowSeconds.
func RateLimit(maxRequests int, windowSeconds int) gin.HandlerFunc {
	window := time.Duration(windowSeconds) * time.Second
	return func(c *gin.Context) {
		key := c.ClientIP() + ":" + c.FullPath()
		if !allowKey(key, maxRequests, window) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many requests, please try again later"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// AllowByKey lets handlers enforce a per-(custom-key) rate limit — e.g. per
// email or per account, layered on top of the per-IP middleware. Returns
// false when the quota is exhausted; caller should respond 429 and abort.
//
// Use a namespaced key like "login:" + email or "reset:" + email so unrelated
// limits do not collide. Pair with the IP-keyed middleware, never replace it.
func AllowByKey(key string, maxRequests int, windowSeconds int) bool {
	return allowKey(key, maxRequests, time.Duration(windowSeconds)*time.Second)
}

// Cleanup removes stale entries periodically. Call once at startup.
func StartRateLimitCleanup() {
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			limiter.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for key, times := range limiter.requests {
				valid := make([]time.Time, 0)
				for _, t := range times {
					if t.After(cutoff) {
						valid = append(valid, t)
					}
				}
				if len(valid) == 0 {
					delete(limiter.requests, key)
				} else {
					limiter.requests[key] = valid
				}
			}
			limiter.mu.Unlock()
		}
	}()
}
