package handlers_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

func TestWS_RuntimeGameBoundaryAndRefresh(t *testing.T) {
	_, _, url := newWSServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	player := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameA := seedMultiplayerGame(t, owner.ID)
	gameB := seedMultiplayerGame(t, owner.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	host := dialWS(t, ctx, url, owner)
	if err := wsjson.Write(ctx, host, lobby.ClientMsg{Type: "create", GameID: gameB}); err != nil {
		t.Fatal(err)
	}
	code := expectFrame(t, ctx, host, "lobby").Lobby.Code
	tok, raw, err := models.MintGameSessionToken(player.ID, gameA, "")
	if err != nil {
		t.Fatal(err)
	}
	// External web origins are allowed only with an explicit valid runtime token.
	conn, _, err := websocket.Dial(ctx, url+"?token="+raw, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://external-game.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for _, spectator := range []bool{false, true} {
		if err := wsjson.Write(ctx, conn, lobby.ClientMsg{Type: "join", Code: code, Spectator: spectator}); err != nil {
			t.Fatal(err)
		}
		expectFrame(t, ctx, conn, "error")
	}
	if err := wsjson.Write(ctx, conn, lobby.ClientMsg{Type: "create", GameID: gameA}); err != nil {
		t.Fatal(err)
	}
	expectFrame(t, ctx, conn, "lobby")
	next, replacement, err := models.MintGameSessionToken(player.ID, gameA, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, conn, lobby.ClientMsg{Type: "auth", Token: replacement}); err != nil {
		t.Fatal(err)
	}
	expectFrame(t, ctx, conn, "authenticated")
	if err := models.RevokeGameSessionToken(tok.ID, player.ID); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, conn, lobby.ClientMsg{Type: "ready", Ready: true}); err != nil {
		t.Fatal(err)
	}
	expectFrame(t, ctx, conn, "lobby")
	if err := models.RevokeGameSessionToken(next.ID, player.ID); err != nil {
		t.Fatal(err)
	}
	// Idle connections are closed too, without waiting for another command.
	var msg lobby.ServerMsg
	if err := wsjson.Read(ctx, conn, &msg); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("revoked connection stayed alive: frame=%+v err=%v", msg, err)
	}
}

func TestWS_RuntimeRejectsExpiredScopeAndIdentity(t *testing.T) {
	_, _, url := newWSServer(t)
	u := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	game := seedMultiplayerGame(t, u.ID)
	otherGame := seedMultiplayerGame(t, u.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, scenario := range []string{"expired", "revoked", "scope", "refresh-game"} {
		t.Run(scenario, func(t *testing.T) {
			tok, raw, err := models.MintGameSessionToken(u.ID, game, "")
			if err != nil {
				t.Fatal(err)
			}
			conn, _, err := websocket.Dial(ctx, url+"?token="+raw, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			switch scenario {
			case "expired":
				_, err = storage.DB.Exec(`UPDATE game_session_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, tok.ID)
			case "revoked":
				err = models.RevokeGameSessionToken(tok.ID, u.ID)
			case "scope":
				_, err = storage.DB.Exec(`UPDATE game_session_tokens SET scopes='reserved' WHERE id=?`, tok.ID)
			case "refresh-game":
				_, replacement, e := models.MintGameSessionToken(u.ID, otherGame, "")
				if e != nil {
					t.Fatal(e)
				}
				err = wsjson.Write(ctx, conn, lobby.ClientMsg{Type: "auth", Token: replacement})
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "refresh-game" {
				_ = wsjson.Write(ctx, conn, lobby.ClientMsg{Type: "create", GameID: game})
			}
			var msg lobby.ServerMsg
			if err := wsjson.Read(ctx, conn, &msg); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("expected rejection: %+v %v", msg, err)
			}
		})
	}
}

func TestWS_NativeBearerAndHandshakeScope(t *testing.T) {
	_, _, url := newWSServer(t)
	u := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	game := seedMultiplayerGame(t, u.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, scope := range []string{"session:write", "reserved"} {
		_, raw, err := models.MintGameSessionToken(u.ID, game, scope)
		if err != nil {
			t.Fatal(err)
		}
		conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + raw}}})
		if scope == "reserved" {
			if err == nil {
				conn.CloseNow()
				t.Fatal("reserved scope granted access")
			}
			if response == nil || response.StatusCode != http.StatusForbidden {
				t.Fatalf("expected scope rejection: %v", response)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Write(ctx, conn, lobby.ClientMsg{Type: "create", GameID: game}); err != nil {
			t.Fatal(err)
		}
		expectFrame(t, ctx, conn, "lobby")
		conn.Close(websocket.StatusNormalClosure, "")
	}
}
