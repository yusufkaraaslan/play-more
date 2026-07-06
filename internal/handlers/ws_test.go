package handlers_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"

	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// newWSServer wires a real TCP test server with the /ws route mounted
// exactly like production (rate limit + AuthOptional + AuthRequired)
// on a fresh hub, and returns the ws:// URL.
func newWSServer(t *testing.T) (*testutil.TestServer, *lobby.Hub, string) {
	t.Helper()
	testutil.ResetRateLimits()
	ts := testutil.NewTestServer(t)
	hub := lobby.NewHub()
	ts.Engine.GET("/ws", middleware.RateLimit(30, 60), middleware.AuthOptional(), middleware.AuthRequired(), handlers.GameLobbyWS(hub))
	srv := httptest.NewServer(ts.Engine)
	t.Cleanup(srv.Close)
	return ts, hub, "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

// sessionCookie seeds a session row for the user and returns the
// Cookie header value (mirrors testutil.WithAuth, which only works
// for in-process httptest recorders, not a real dial).
func sessionCookie(t *testing.T, user *models.User) string {
	t.Helper()
	token := uuid.NewString()
	hash := models.HashSessionToken(token)
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := storage.DB.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`, hash, user.ID, expires); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return "session=" + token
}

func dialWS(t *testing.T, ctx context.Context, url string, user *models.User) *websocket.Conn {
	t.Helper()
	h := http.Header{}
	h.Set("Cookie", sessionCookie(t, user))
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

// expectFrame reads frames until one of the wanted type arrives
// (skipping interleaved lobby snapshots etc.).
func expectFrame(t *testing.T, ctx context.Context, conn *websocket.Conn, wantType string) lobby.ServerMsg {
	t.Helper()
	for i := 0; i < 10; i++ {
		var m lobby.ServerMsg
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read (waiting for %q): %v", wantType, err)
		}
		if m.Type == wantType {
			return m
		}
	}
	t.Fatalf("no %q frame in 10 reads", wantType)
	return lobby.ServerMsg{}
}

func seedMultiplayerGame(t *testing.T, ownerID string) string {
	t.Helper()
	id := testutil.SeedGame(t, nil, ownerID, "MP Game "+uuid.NewString()[:8])
	if _, err := storage.DB.Exec(`UPDATE games SET multiplayer = 1 WHERE id = ?`, id); err != nil {
		t.Fatalf("flag multiplayer: %v", err)
	}
	return id
}

func TestWS_RequiresAuth(t *testing.T) {
	_, _, url := newWSServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		t.Fatal("unauthenticated dial succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 handshake, got %+v", resp)
	}
}

func TestWS_RejectsCrossOrigin(t *testing.T) {
	ts, _, url := newWSServer(t)
	_ = ts
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Cookie", sessionCookie(t, user))
	h.Set("Origin", "https://evil.example.com")
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
	if err == nil {
		t.Fatal("cross-origin dial succeeded — CSWSH protection missing")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("expected 403 handshake, got %d", code)
	}
}

func TestWS_SameOriginAllowed(t *testing.T) {
	_, _, url := newWSServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Cookie", sessionCookie(t, user))
	// Same host as the request target — the browser case.
	h.Set("Origin", "http"+strings.TrimSuffix(strings.TrimPrefix(url, "ws"), "/ws"))
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("same-origin dial failed: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

func TestWS_FullLobbyFlow(t *testing.T) {
	_, _, url := newWSServer(t)
	hostUser := testutil.SeedUser(t, nil, testutil.SeedUserOpts{Username: "hostuser", Email: "host@x.dev", EmailVerified: true})
	guestUser := testutil.SeedUser(t, nil, testutil.SeedUserOpts{Username: "guestuser", Email: "guest@x.dev", EmailVerified: true})
	gameID := seedMultiplayerGame(t, hostUser.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	host := dialWS(t, ctx, url, hostUser)
	guest := dialWS(t, ctx, url, guestUser)

	// Host creates a lobby.
	if err := wsjson.Write(ctx, host, lobby.ClientMsg{Type: "create", GameID: gameID}); err != nil {
		t.Fatal(err)
	}
	state := expectFrame(t, ctx, host, "lobby")
	code := state.Lobby.Code
	if code == "" || state.Lobby.GameID != gameID || state.Lobby.HostID != hostUser.ID {
		t.Fatalf("bad create state: %+v", state.Lobby)
	}

	// Guest joins (lower-cased code must work).
	if err := wsjson.Write(ctx, guest, lobby.ClientMsg{Type: "join", Code: strings.ToLower(code)}); err != nil {
		t.Fatal(err)
	}
	gState := expectFrame(t, ctx, guest, "lobby")
	if len(gState.Lobby.Players) != 2 {
		t.Fatalf("guest sees %d players, want 2", len(gState.Lobby.Players))
	}

	// Start before ready → error frame to host.
	if err := wsjson.Write(ctx, host, lobby.ClientMsg{Type: "start"}); err != nil {
		t.Fatal(err)
	}
	if e := expectFrame(t, ctx, host, "error"); e.Error == "" {
		t.Fatal("expected not-ready error")
	}

	// Guest readies up; host starts; both get launch.
	if err := wsjson.Write(ctx, guest, lobby.ClientMsg{Type: "ready", Ready: true}); err != nil {
		t.Fatal(err)
	}
	expectFrame(t, ctx, host, "lobby") // ready-state broadcast
	if err := wsjson.Write(ctx, host, lobby.ClientMsg{Type: "start"}); err != nil {
		t.Fatal(err)
	}
	if l := expectFrame(t, ctx, host, "launch"); !l.Lobby.Started {
		t.Fatal("host launch frame not started")
	}
	if l := expectFrame(t, ctx, guest, "launch"); !l.Lobby.Started {
		t.Fatal("guest launch frame not started")
	}

	// Relay host → everyone.
	if err := wsjson.Write(ctx, host, lobby.ClientMsg{Type: "msg", Data: []byte(`{"move":"e4"}`)}); err != nil {
		t.Fatal(err)
	}
	relayed := expectFrame(t, ctx, guest, "msg")
	if relayed.From != hostUser.ID || string(relayed.Data) != `{"move":"e4"}` {
		t.Fatalf("bad relay: from=%q data=%s", relayed.From, relayed.Data)
	}

	// Targeted relay guest → host.
	if err := wsjson.Write(ctx, guest, lobby.ClientMsg{Type: "msg", To: hostUser.ID, Data: []byte(`"pong"`)}); err != nil {
		t.Fatal(err)
	}
	back := expectFrame(t, ctx, host, "msg")
	if back.From != guestUser.ID || string(back.Data) != `"pong"` {
		t.Fatalf("bad targeted relay: %+v", back)
	}

	// Host disconnects → guest is told the lobby closed.
	host.Close(websocket.StatusNormalClosure, "")
	closed := expectFrame(t, ctx, guest, "closed")
	if closed.Reason != "host_left" {
		t.Fatalf("closed reason = %q, want host_left", closed.Reason)
	}
}

func TestWS_CreateRejectsNonMultiplayerAndForeignUnpublished(t *testing.T) {
	_, _, url := newWSServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{Username: "owner2", Email: "owner2@x.dev", EmailVerified: true})
	rando := testutil.SeedUser(t, nil, testutil.SeedUserOpts{Username: "rando2", Email: "rando2@x.dev", EmailVerified: true})

	// Published but NOT multiplayer.
	plainGame := testutil.SeedGame(t, nil, owner.ID, "Plain Game "+uuid.NewString()[:8])
	// Multiplayer but unpublished — invisible to non-owners.
	hiddenGame := seedMultiplayerGame(t, owner.ID)
	if _, err := storage.DB.Exec(`UPDATE games SET published = 0 WHERE id = ?`, hiddenGame); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, url, rando)

	for name, msg := range map[string]lobby.ClientMsg{
		"non-multiplayer game":   {Type: "create", GameID: plainGame},
		"foreign unpublished":    {Type: "create", GameID: hiddenGame},
		"nonexistent game":       {Type: "create", GameID: "no-such-game"},
		"unknown message type":   {Type: "frobnicate"},
		"join with unknown code": {Type: "join", Code: "ZZZZZZ"},
	} {
		if err := wsjson.Write(ctx, conn, msg); err != nil {
			t.Fatalf("%s write: %v", name, err)
		}
		if e := expectFrame(t, ctx, conn, "error"); e.Error == "" {
			t.Fatalf("%s: expected error frame, got %+v", name, e)
		}
	}

	// The owner CAN open a lobby on their own unpublished game.
	ownerConn := dialWS(t, ctx, url, owner)
	if err := wsjson.Write(ctx, ownerConn, lobby.ClientMsg{Type: "create", GameID: hiddenGame}); err != nil {
		t.Fatal(err)
	}
	if s := expectFrame(t, ctx, ownerConn, "lobby"); s.Lobby.GameID != hiddenGame {
		t.Fatalf("owner create failed: %+v", s)
	}
}

func TestWS_OnlineCountInGamePayload(t *testing.T) {
	ts, hub, url := newWSServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{Username: "counter", Email: "counter@x.dev", EmailVerified: true})
	gameID := seedMultiplayerGame(t, user.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, url, user)
	if err := wsjson.Write(ctx, conn, lobby.ClientMsg{Type: "create", GameID: gameID}); err != nil {
		t.Fatal(err)
	}
	expectFrame(t, ctx, conn, "lobby")

	if got := hub.OnlineCount(gameID); got != 1 {
		t.Fatalf("hub count = %d, want 1", got)
	}

	// GetGame reports online_players from lobby.Default — the test hub is
	// separate, so assert the payload field exists for multiplayer games
	// (count 0 on the default hub) and the flag round-trips.
	w, body := ts.Do(t, "GET", "/api/v1/games/"+gameID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get game: %d %s", w.Code, body)
	}
	var payload struct {
		Game struct {
			Multiplayer bool `json:"multiplayer"`
		} `json:"game"`
		OnlinePlayers *int `json:"online_players"`
	}
	testutil.DecodeJSON(t, body, &payload)
	if !payload.Game.Multiplayer {
		t.Fatal("multiplayer flag lost in game payload")
	}
	if payload.OnlinePlayers == nil {
		t.Fatal("online_players missing from multiplayer game payload")
	}
	_ = fmt.Sprintf("%d", *payload.OnlinePlayers)
}
