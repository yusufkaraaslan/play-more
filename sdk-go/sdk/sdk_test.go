package sdk_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/sdk-go/sdk"
)

func TestClient_ListGames(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"games":[{"id":"g1","title":"Hello","genre":"action"}]}`)
	}))
	defer srv.Close()

	c := sdk.New("pm_k_test")
	c.BaseURL = srv.URL
	games, err := c.ListGames(context.Background())
	if err != nil {
		t.Fatalf("ListGames: %v", err)
	}
	if len(games) != 1 || games[0].ID != "g1" {
		t.Errorf("unexpected games: %+v", games)
	}
	if gotPath != "/games" {
		t.Errorf("path: got %q want /games", gotPath)
	}
	if gotAuth != "Bearer pm_k_test" {
		t.Errorf("auth: got %q", gotAuth)
	}
}

func TestClient_UploadGame(t *testing.T) {
	var gotContentType string
	var gotFields map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		gotFields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				gotFields[k] = v[0]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"game":{"id":"new_id","title":"My Game"}}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	gameFile := filepath.Join(dir, "game.html")
	if err := os.WriteFile(gameFile, []byte("<html>hi</html>"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(gameFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	c := sdk.New("pm_k_test")
	c.BaseURL = srv.URL
	g, err := c.UploadGame(context.Background(), sdk.GameUploadOptions{
		Title:    "My Game",
		Genre:    "action",
		GameFile: f,
	})
	if err != nil {
		t.Fatalf("UploadGame: %v", err)
	}
	if g == nil || g.ID != "new_id" {
		t.Errorf("game: %+v", g)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data") {
		t.Errorf("content-type: %q", gotContentType)
	}
	if gotFields["title"] != "My Game" {
		t.Errorf("title field: %q", gotFields["title"])
	}
	if gotFields["genre"] != "action" {
		t.Errorf("genre field: %q", gotFields["genre"])
	}
}

func TestClient_UploadChunked(t *testing.T) {
	// 64 KiB file split into two 32 KiB chunks (server returns
	// chunk_size=32 KiB in this fake). The test confirms init,
	// both chunk PUTs, and finalize fire, and the SHA is
	// forwarded.
	dir := t.TempDir()
	path := filepath.Join(dir, "game.zip")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x42}, 64*1024), 0644); err != nil {
		t.Fatal(err)
	}
	sha, err := sdk.FileSHA256(path)
	if err != nil {
		t.Fatalf("FileSHA256: %v", err)
	}

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "POST" && r.URL.Path == "/uploads/init":
			_, _ = io.WriteString(w, `{"upload_id":"u1","chunk_size":32768,"expires_at":"2030-01-01T00:00:00Z"}`)
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/uploads/u1/chunks"):
			w.WriteHeader(200)
		case r.Method == "POST" && r.URL.Path == "/uploads/u1/finalize":
			var body struct {
				SHA256 string `json:"sha256"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.SHA256 != sha {
				t.Errorf("finalize sha: got %q want %q", body.SHA256, sha)
			}
			_, _ = io.WriteString(w, `{"game_id":"newgame"}`)
		}
	}))
	defer srv.Close()

	c := sdk.New("pm_k_test")
	c.BaseURL = srv.URL
	res, err := c.UploadChunked(context.Background(), sdk.ChunkedUploadOptions{
		Path:     path,
		Size:     64 * 1024,
		Filename: "game.zip",
		Kind:     "new_game",
		Metadata: map[string]any{"title": "x", "genre": "action"},
		SHA256:   sha,
	})
	if err != nil {
		t.Fatalf("UploadChunked: %v", err)
	}
	if res.GameID != "newgame" {
		t.Errorf("game_id: %q", res.GameID)
	}
	// 1 init + 2 chunk PUTs + 1 finalize = 4 calls.
	if len(calls) != 4 {
		t.Errorf("calls: got %d (%v) want 4", len(calls), calls)
	}
}

func TestVerifySignature(t *testing.T) {
	secret := "pm_k_supersecret"
	body := []byte(`{"event":"game.published","x":1}`)
	sig := sdk.SignBody(secret, body)
	if err := sdk.VerifySignature(secret, body, sig); err != nil {
		t.Errorf("VerifySignature(valid): %v", err)
	}
	// Wrong secret → error.
	if err := sdk.VerifySignature("other", body, sig); err == nil {
		t.Error("expected error on wrong secret")
	}
	// Missing prefix → error.
	if err := sdk.VerifySignature(secret, body, hex.EncodeToString([]byte("x"))); err == nil {
		t.Error("expected error on bad header")
	}
	// Empty secret → error.
	if err := sdk.VerifySignature("", body, sig); err == nil {
		t.Error("expected error on empty secret")
	}
}
