package server

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// =============================================================================
// Server setup
// =============================================================================

func New(frontendFS embed.FS, goatCounterURL, gamesDomain, baseURL, trustedProxies string) *gin.Engine {
	r := gin.New()
	// Custom recovery — logs the panic value and stack to stderr but does
	// NOT dump HTTP headers (gin.Recovery's secureRequestDump leaks the
	// session Cookie to log aggregators on panic).
	r.Use(gin.CustomRecoveryWithWriter(os.Stderr, func(c *gin.Context, recovered any) {
		log.Printf("panic recovered: %v\n%s", recovered, debug.Stack())
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	r.MaxMultipartMemory = 32 << 20

	// =========================================================================
	// Global middleware
	// =========================================================================

	// Custom logger — strips query strings so tokens don't land in logs.
	r.Use(gin.LoggerWithFormatter(func(p gin.LogFormatterParams) string {
		path := strings.ReplaceAll(p.Path, "\n", "")
		path = strings.ReplaceAll(path, "\r", "")
		return fmt.Sprintf("%s | %3d | %13v | %15s | %-7s %s\n",
			p.TimeStamp.Format("2006/01/02 - 15:04:05"),
			p.StatusCode, p.Latency, p.ClientIP, p.Method, path,
		)
	}))

	// Trust proxies
	hasTrustedProxy := false
	if trustedProxies != "" {
		proxies := []string{}
		for _, p := range strings.Split(trustedProxies, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if p == "0.0.0.0/0" || p == "::/0" {
				panic("trusted-proxies must not include 0.0.0.0/0 or ::/0")
			}
			proxies = append(proxies, p)
		}
		if len(proxies) > 0 {
			r.SetTrustedProxies(proxies)
			middleware.SetTrustedProxies(proxies)
			hasTrustedProxy = true
		} else {
			r.SetTrustedProxies(nil)
			middleware.SetTrustedProxies(nil)
		}
	} else {
		r.SetTrustedProxies(nil)
	}

	// HTTPS redirect (before security headers)
	r.Use(func(c *gin.Context) {
		if hasTrustedProxy && c.Request.Header.Get("X-Forwarded-Proto") == "http" {
			scheme := "https"
			host := c.Request.Host
			if baseURL != "" {
				if i := strings.Index(baseURL, "://"); i != -1 {
					host = baseURL[i+3:]
					scheme = baseURL[:i]
				}
			}
			target := scheme + "://" + host + c.Request.URL.Path
			if len(c.Request.URL.RawQuery) > 0 {
				target += "?" + c.Request.URL.RawQuery
			}
			c.Redirect(http.StatusMovedPermanently, target)
			c.Abort()
			return
		}
		c.Next()
	})

	r.Use(securityHeaders(goatCounterURL))
	r.Use(cacheHeaders())

	// JSON body size limit
	r.Use(func(c *gin.Context) {
		ct := c.GetHeader("Content-Type")
		if strings.Contains(ct, "application/json") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		}
		c.Next()
	})

	// Gzip (skip game file serving; skip /ws — the upgrade needs the raw
	// http.Hijacker, not a compressing writer wrapper)
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/play/", "/ws"})))

	// Site analytics
	r.Use(middleware.TrackPageView())

	// =========================================================================
	// Health checks
	// =========================================================================

	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/ready", func(c *gin.Context) {
		if err := storage.DB.Ping(); err != nil {
			log.Printf("/ready DB ping failed: %v", err)
			c.JSON(503, gin.H{"status": "not ready"})
			return
		}
		c.JSON(200, gin.H{"status": "ready"})
	})

	// =========================================================================
	// API routes — mounted under both /api/v1 (canonical) and /api
	// (permanent alias for backward compatibility). See mountAPIRoutes
	// in routes.go for the route table and the rationale.
	// =========================================================================

	cfg := apiConfig{
		uploadCap:   int64(storage.MaxFileSize) + (32 << 20),
		imageCap:    int64(10 << 20),
		chunkPutCap: int64((8 << 20) + (1 << 20)), // 8 MiB chunk + 1 MiB headroom
	}
	mountAPIRoutes(r.Group("/api/v1"), cfg)
	mountAPIRoutes(r.Group("/api"), cfg)

	// Serve uploaded images. r.Static would expose directory listings
	// (http.FileServer behavior); wrap with a handler that 404s any path
	// resolving to a directory so /uploads/<userID>/ doesn't enumerate files.
	uploadsDir := filepath.Join(storage.GamesDir, "..", "uploads")
	uploadsFS := http.Dir(uploadsDir)
	r.GET("/uploads/*filepath", func(c *gin.Context) {
		fp := c.Param("filepath")
		if fp == "" || strings.HasSuffix(fp, "/") {
			c.String(http.StatusNotFound, "not found")
			return
		}
		f, err := uploadsFS.Open(fp)
		if err != nil {
			c.String(http.StatusNotFound, "not found")
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			c.String(http.StatusNotFound, "not found")
			return
		}
		// Defense-in-depth: /uploads/ serves user-uploaded image content.
		// Image validation (UploadImage → ValidateImageBytes) rejects anything
		// that doesn't decode as PNG/JPG/GIF, but if that validation is ever
		// loosened or another code path drops a file here, these headers
		// prevent the file from being abused as a script-executing document.
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "default-src 'none'; img-src 'self' data: blob:; style-src 'unsafe-inline'; sandbox")
		c.Header("Cross-Origin-Opener-Policy", "same-origin")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		http.ServeContent(c.Writer, c.Request, stat.Name(), stat.ModTime(), f)
	})

	// Self-hosted avatar generation
	r.GET("/avatar/:username", middleware.RateLimit(120, 60), handlers.GetAvatar)

	// API documentation — Swagger UI served from a CDN, with a
	// /openapi.yaml machine-readable spec for tooling and offline
	// editors. See handlers/openapi_handlers.go for the CSP override.
	r.GET("/docs", middleware.RateLimit(60, 60), handlers.ServeAPIDocs)
	r.GET("/openapi.yaml", middleware.AuthRequired(), handlers.AdminRequired(), handlers.ServeOpenAPISpec)

	// Deploy script download
	r.GET("/deploy.sh", middleware.RateLimit(10, 60), handlers.ServeDeployScript)

	// Multiplayer client shim — games <script src> this to speak the
	// lobby postMessage protocol. Served on both the main and
	// --games-domain origins (same engine), so a game loads it with a
	// relative /playmore-mp.js regardless of which origin it runs on.
	r.GET("/playmore-mp.js", handlers.ServeMPScript)

	// Multiplayer lobby WebSocket (#29). Root-mounted (not under /api —
	// it is not a REST endpoint and the OpenAPI drift test ignores it).
	// CSRF-equivalent protection is the Origin check inside the
	// handler's websocket.Accept; see handlers/ws.go.
	r.GET("/ws", middleware.RateLimit(30, 60), middleware.WSQueryTokenAuth(), middleware.AuthOptional(), middleware.AuthRequiredOrGameSession(), handlers.GameLobbyWS(lobby.Default))

	// Game file serving (for iframe player)
	// Game iframe content. spaOrigin gates who can embed via CSP frame-ancestors —
	// XFO can't whitelist a cross-origin host, so split-origin (games.* subdomain) needs CSP.
	spaOrigin := strings.TrimRight(baseURL, "/")
	r.GET("/play/:id", handlers.ServeGameFiles(spaOrigin, gamesDomain))
	r.GET("/play/:id/*filepath", handlers.ServeGameFiles(spaOrigin, gamesDomain))

	// =========================================================================
	// Frontend (SPA)
	// =========================================================================

	frontendSub, err := fs.Sub(frontendFS, "frontend")
	if err == nil {
		r.StaticFS("/assets", http.FS(frontendSub))

		wellKnownSub, err := fs.Sub(frontendFS, "frontend/.well-known")
		if err == nil {
			r.StaticFS("/.well-known", http.FS(wellKnownSub))
		}

		r.NoRoute(spaFallback(frontendFS, goatCounterURL, gamesDomain, baseURL))
	}

	return r
}
