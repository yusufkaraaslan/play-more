package server

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"html"
	"net/http"

	"github.com/gin-gonic/gin"
)

// spaFallback returns a NoRoute handler that serves the embedded index.html
// with per-request CSP nonce injection. It rejects API and play routes so they
// get proper 404s instead of the SPA shell.
func spaFallback(frontendFS embed.FS, goatCounterURL, gamesDomain, baseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		// Cover/screenshot images live under /play/* and are loaded directly from
		// the games origin when split-origin is configured (#1 fix), so img-src /
		// media-src must whitelist it.
		gamesSrc := ""
		if gamesOrigin != "" {
			frameSrc += " " + gamesOrigin
			gamesSrc = " " + gamesOrigin
		}
		csp := "default-src 'self'; script-src 'self' 'nonce-" + nonce + "'; script-src-attr 'unsafe-inline'; style-src 'self' 'nonce-" + nonce + "' https://fonts.googleapis.com; style-src-attr 'unsafe-inline'; img-src 'self' data: blob: https://img.youtube.com" + gamesSrc + "; connect-src 'self'; frame-src " + frameSrc + "; media-src 'self'" + gamesSrc + "; font-src 'self' https://fonts.googleapis.com https://fonts.gstatic.com; object-src 'none'; base-uri 'self'; form-action 'self'"
		if gcStr != "" {
			csp = "default-src 'self'; script-src 'self' 'nonce-" + nonce + "' https://gc.zgo.at https://*.goatcounter.com https://static.cloudflareinsights.com; script-src-attr 'unsafe-inline'; style-src 'self' 'nonce-" + nonce + "' https://fonts.googleapis.com; style-src-attr 'unsafe-inline'; img-src 'self' data: blob: https://img.youtube.com https://gc.zgo.at" + gamesSrc + "; connect-src 'self' https://*.goatcounter.com https://cloudflareinsights.com; frame-src " + frameSrc + "; media-src 'self'" + gamesSrc + "; font-src 'self' https://fonts.googleapis.com https://fonts.gstatic.com; object-src 'none'; base-uri 'self'; form-action 'self'"
		}
		c.Header("Content-Security-Policy", csp)

		// Inject GoatCounter script if configured
		if gcStr != "" {
			escaped := html.EscapeString(gcStr)
			snippet := []byte(`<script nonce="` + nonce + `" data-goatcounter="` + escaped + `/count" async src="//gc.zgo.at/count.js"></script></body>`)
			data = bytes.Replace(data, []byte("</body>"), snippet, 1)
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}
}

// securityHeaders is a middleware that sets baseline security headers on every
// response. The SPA handler overrides CSP with a nonce-based policy; this
// provides a safe default for non-HTML responses (API, static files, etc.).
func securityHeaders(goatCounterURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("goatcounter_url", goatCounterURL)
		c.Header("Content-Security-Policy", "default-src 'self'; object-src 'none'")
		if c.Request.Header.Get("X-Forwarded-Proto") == "https" || c.Request.TLS != nil {
			// `preload` is intentionally omitted — submit the domain to
			// hstspreload.org first, then add it back.
			c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		c.Header("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
		c.Next()
	}
}

// cacheHeaders is a middleware that sets Cache-Control based on the request path.
func cacheHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		// API routes: no-store, no-cache, must-revalidate
		if len(path) >= 4 && path[:4] == "/api" {
			c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		} else if len(path) >= 8 && path[:8] == "/assets/" {
			// Static assets: immutable, long-lived
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// HTML pages and other routes: no-cache
			c.Header("Cache-Control", "no-cache")
		}
		c.Next()
	}
}
