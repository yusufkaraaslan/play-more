package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/yusufkaraaslan/play-more/internal/email"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/server"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

var base string

func main() {
	tmpDir, _ := os.MkdirTemp("", "playmore-test-*")
	defer os.RemoveAll(tmpDir)

	if err := storage.InitDB(tmpDir); err != nil {
		die("InitDB: %v", err)
	}
	if err := storage.InitFileStorage(tmpDir); err != nil {
		die("InitFileStorage: %v", err)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("testpassword123"), 12)
	storage.DB.Exec(`INSERT INTO users (id, username, email, password, is_developer, email_verified, created_at) VALUES (?, 'admin', 'admin@test.com', ?, 1, 1, datetime('now'))`,
		"usr-admin-001", string(hash))

	middleware.StartRateLimitCleanup()
	middleware.StartAnalyticsWriter()
	email.CurrentConfig = &email.Config{}
	lobby.Default.StartCleanup(middleware.ShutdownCh)
	server.RTCIceServers = server.ParseIceServers("stun:stun.l.google.com:19302", "")

	r := server.New(embed.FS{}, "", "", "http://localhost:18080", "")
	srv := &http.Server{Handler: r, Addr: "127.0.0.1:18080"}
	go srv.ListenAndServe()
	time.Sleep(500 * time.Millisecond)
	base = "http://localhost:18080"
	defer srv.Close()

	cookies := loginAs("admin@test.com", "testpassword123")

	log("Seed")
	post("/api/v1/seed", `{}`, cookies)

	body := get("/api/v1/games?limit=20", cookies)
	var gamesResp struct {
		Games []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Multiplayer bool   `json:"multiplayer"`
		} `json:"games"`
	}
	json.Unmarshal([]byte(body), &gamesResp)

	var mpID string
	for _, g := range gamesResp.Games {
		fmt.Printf("  %-25s | mp=%-5v | id=%s\n", g.Title, g.Multiplayer, g.ID[:12])
		if g.Title == "MP Test Arena" {
			mpID = g.ID
		}
	}
	if mpID == "" {
		die("MP Test Arena not found")
	}

	log("Mint pm_gs_ token")
	tokBody := post("/api/v1/games/"+mpID+"/sdk-token", `{}`, cookies)
	var tokResp struct {
		Token   string `json:"token"`
		TokenID string `json:"token_id"`
	}
	json.Unmarshal([]byte(tokBody), &tokResp)
	mpToken := tokResp.Token
	fmt.Printf("  Token: %s...\n", mpToken[:20])

	log("Open play session (cookie)")
	psBody := post("/api/v1/games/"+mpID+"/play-sessions", `{}`, cookies)
	var psResp struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal([]byte(psBody), &psResp)
	psID := psResp.SessionID
	fmt.Printf("  Session: %s...\n", psID[:12])

	log("Heartbeat (cookie)")
	post("/api/v1/play-sessions/"+psID+"/heartbeat", `{}`, cookies)

	log("Heartbeat (pm_gs_ token)")
	postBearer("/api/v1/play-sessions/"+psID+"/heartbeat", mpToken)

	log("online_players (should be 1)")
	checkOnline(mpID, 1)

	log("Open play session (pm_gs_ token)")
	postBearer("/api/v1/games/"+mpID+"/play-sessions", mpToken)

	log("online_players (should be 2)")
	checkOnline(mpID, 2)

	log("End play session")
	post("/api/v1/play-sessions/"+psID+"/end", `{}`, cookies)

	log("RTC config")
	fmt.Printf("  %s\n", get("/rtc-config", nil))

	log("CORS preflight (Origin: null)")
	preflight("/api/v1/games/" + mpID + "/play-sessions")

	// Demo user for SDK key tests
	storage.DB.Exec(`UPDATE users SET password=?, email_verified=1 WHERE username='playmore'`, string(hash))
	demoCookies := loginAs("demo@playmore.dev", "testpassword123")

	log("Create SDK key (demo user = owner)")
	sdkBody := post("/api/v1/games/"+mpID+"/sdk-keys", `{"name":"Test CI Key"}`, demoCookies)
	var sdkResp struct {
		RawKey string `json:"raw_key"`
	}
	json.Unmarshal([]byte(sdkBody), &sdkResp)
	if sdkResp.RawKey == "" {
		fmt.Printf("  ERROR: empty raw_key — response: %s\n", sdkBody)
	} else {
		fmt.Printf("  Key: %s...\n", sdkResp.RawKey[:14])
	}

	log("List SDK keys")
	listBody := get("/api/v1/games/"+mpID+"/sdk-keys", demoCookies)
	var listResp struct {
		Keys []struct {
			Name string `json:"name"`
		} `json:"keys"`
	}
	json.Unmarshal([]byte(listBody), &listResp)
	fmt.Printf("  Keys: %d\n", len(listResp.Keys))

	if sdkResp.RawKey != "" {
		log("SDK key (pm_gk_) rejected on /auth/me")
		code := getBearerCode("/api/v1/auth/me", sdkResp.RawKey)
		fmt.Printf("  Status: %d (expect 403)\n", code)
	}

	log("pm_gs_ token rejected on /auth/me")
	code := getBearerCode("/api/v1/auth/me", mpToken)
	fmt.Printf("  Status: %d (expect 403)\n", code)

	log("WebSocket no-auth")
	code = getCode("/ws")
	fmt.Printf("  Status: %d (expect 401)\n", code)

	log("WebSocket with pm_gs_ token")
	code = wsUpgrade("/ws?token=" + url.QueryEscape(mpToken))
	fmt.Printf("  Status: %d (expect 101)\n", code)

	fmt.Println("\n=== ALL TESTS PASSED ===")
}

func checkOnline(gameID string, expect int) {
	body := get("/api/v1/games/"+gameID, nil)
	var resp map[string]any
	json.Unmarshal([]byte(body), &resp)
	fmt.Printf("  online_players = %v (expect %d)\n", resp["online_players"], expect)
}

func loginAs(email, password string) []*http.Cookie {
	body := fmt.Sprintf(`{"email":"%s","password":"%s"}`, email, password)
	req, _ := http.NewRequest("POST", base+"/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		die("login: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.Cookies()
}

func get(path string, c []*http.Cookie) string {
	req, _ := http.NewRequest("GET", base+path, nil)
	for _, ck := range c {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		die("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func post(path, body string, c []*http.Cookie) string {
	req, _ := http.NewRequest("POST", base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	for _, ck := range c {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		die("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func postBearer(path, token string) string {
	req, _ := http.NewRequest("POST", base+path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		die("POST bearer %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func getBearerCode(path, token string) int {
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func getCode(path string) int {
	resp, err := http.DefaultClient.Get(base + path)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func preflight(path string) {
	req, _ := http.NewRequest("OPTIONS", base+path, nil)
	req.Header.Set("Origin", "null")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization,Content-Type")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		die("preflight: %v", err)
	}
	defer resp.Body.Close()
	fmt.Printf("  Status: %d (expect 204)\n", resp.StatusCode)
	for k, v := range resp.Header {
		if strings.Contains(strings.ToLower(k), "access-control") {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
}

func wsUpgrade(path string) int {
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "13")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func log(step string) {
	fmt.Printf("\n=== %s ===\n", step)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}
