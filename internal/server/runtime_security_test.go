package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

func TestAccessLogOmitsCredentials(t *testing.T) {
	var logs bytes.Buffer
	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{Formatter: accessLogFormatter, Output: &logs}))
	r.GET("/ws", func(c *gin.Context) { c.Status(http.StatusUnauthorized) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ws?token=pm_gs_SECRET&other=private", nil))
	if strings.Contains(logs.String(), "SECRET") || strings.Contains(logs.String(), "private") {
		t.Fatal("query credentials leaked into access log")
	}
	if !strings.Contains(logs.String(), "/ws") {
		t.Fatal("request path missing")
	}
}

func TestRTCConfigGameTokenAndCORS(t *testing.T) {
	r := gin.New()
	r.Use(middleware.CORS())
	r.Use(func(c *gin.Context) {
		c.Set(middleware.UserKey, &models.User{ID: "player", Username: "player"})
		c.Set("auth_method", "game_session")
		c.Set("game_session_token", &models.GameSessionToken{GameID: "game", Scopes: "session:write"})
		c.Next()
	})
	mountRTCConfig(r)
	for _, method := range []string{"OPTIONS", "GET"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/rtc-config", nil)
		req.Header.Set("Origin", "https://external.example")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
			t.Fatalf("%s status %d: %s", method, w.Code, w.Body.String())
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatal("missing CORS")
		}
		if method == "GET" && (!strings.Contains(w.Body.String(), `"game_id":"game"`) || w.Header().Get("Cache-Control") != "no-store") {
			t.Fatalf("invalid runtime config: %s", w.Body.String())
		}
	}
}
