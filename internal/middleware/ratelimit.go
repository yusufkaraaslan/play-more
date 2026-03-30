package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
}

var limiter = &rateLimiter{requests: make(map[string][]time.Time)}

// RateLimit returns middleware that limits requests per IP per window.
// maxRequests per windowSeconds.
func RateLimit(maxRequests int, windowSeconds int) gin.HandlerFunc {
	window := time.Duration(windowSeconds) * time.Second

	return func(c *gin.Context) {
		ip := c.ClientIP()
		key := ip + ":" + c.FullPath()

		limiter.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-window)

		// Clean old entries
		times := limiter.requests[key]
		valid := make([]time.Time, 0, len(times))
		for _, t := range times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}

		if len(valid) >= maxRequests {
			limiter.mu.Unlock()
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests, please try again later",
			})
			c.Abort()
			return
		}

		valid = append(valid, now)
		limiter.requests[key] = valid
		limiter.mu.Unlock()

		c.Next()
	}
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
