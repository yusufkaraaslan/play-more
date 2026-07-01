package handlers_test

import (
	"archive/zip"
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// TestServe_ReuploadServesNewBuildAndHidesOtherBuilds covers two
// fixes at once:
//   - a reupload's new build is actually served (the stored entry
//     is game-root relative: builds/<id>/index.html), and
//   - a NON-active build under builds/ is NOT publicly reachable
//     by guessing its id.
func TestServe_ReuploadServesNewBuildAndHidesOtherBuilds(t *testing.T) {
	ts := testutil.NewTestServer(t)

	// Serve reads from storage.GamesDir; point it at a real temp dir
	// and mount the /play routes (testutil only mounts /api).
	prevGames := storage.GamesDir
	storage.GamesDir = t.TempDir()
	t.Cleanup(func() { storage.GamesDir = prevGames })
	ts.Engine.GET("/play/:id", handlers.ServeGameFiles("", ""))
	ts.Engine.GET("/play/:id/*filepath", handlers.ServeGameFiles("", ""))

	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "ServeReupload")

	// Reupload a zip whose index.html has a recognizable marker.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, _ := zw.Create("index.html")
	if _, err := fw.Write([]byte("<html>NEWBUILD_MARKER</html>")); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	w, body := ts.DoMultipart(t, "POST", "/api/v1/games/"+gameID+"/reupload", map[string]any{
		"game_file": testutil.FileField{Filename: "game.zip", Content: buf.Bytes()},
	}, testutil.WithAuth(owner))
	if w.Code != http.StatusOK {
		t.Fatalf("reupload: %d %s", w.Code, body)
	}

	// The new build must be what /play serves (not stale/404).
	w, body = ts.Do(t, "GET", "/play/"+gameID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("serve reuploaded game: %d %s", w.Code, body)
	}
	if !strings.Contains(string(body), "NEWBUILD_MARKER") {
		t.Errorf("reuploaded content not served, got: %s", body)
	}

	// Plant a decoy build dir on disk and confirm it is NOT reachable
	// via /play/<id>/builds/<decoy>/... — only the active build's
	// subtree may be served.
	decoyDir := storage.BuildDir(gameID, "build_decoy")
	if err := os.MkdirAll(decoyDir, 0o755); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(decoyDir, "index.html"), []byte("SECRET_LEAK"), 0o644); err != nil {
		t.Fatalf("write decoy: %v", err)
	}
	w, body = ts.Do(t, "GET", "/play/"+gameID+"/builds/build_decoy/index.html", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("non-active build must be hidden, got %d", w.Code)
	}
	if strings.Contains(string(body), "SECRET_LEAK") {
		t.Errorf("non-active build content leaked: %s", body)
	}
}

// TestBuilds_ActivateRequiresVerifiedEmail confirms the build
// activate path (which changes the live served content) is gated
// behind email verification, matching every other content-write.
func TestBuilds_ActivateRequiresVerifiedEmail(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: false})
	gameID := testutil.SeedGame(t, nil, owner.ID, "GateActivate")
	buildID := testutil.SeedBuild(t, nil, gameID, owner.ID, "stable")

	w, body := ts.DoAuthed(t, "PUT", "/api/v1/games/"+gameID+"/builds/"+buildID+"/activate", nil, owner)
	if w.Code != http.StatusForbidden {
		t.Errorf("activate by unverified user must be 403, got %d %s", w.Code, body)
	}
}

// TestWebhooks_UpdateEmptyEventsReturns400 confirms a client-side
// mistake (empty events on update) returns 400, not a misleading
// 500.
func TestWebhooks_UpdateEmptyEventsReturns400(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	w, body := ts.Do(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"game.published"},
	}, testutil.WithAuth(owner))
	if w.Code != http.StatusCreated {
		t.Fatalf("create webhook: %d %s", w.Code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	testutil.DecodeJSON(t, body, &created)

	// Update with no events → 400, not 500.
	w, body = ts.Do(t, "PUT", "/api/v1/webhooks/"+created.ID, map[string]any{
		"url":    "https://example.com/hook",
		"active": false,
	}, testutil.WithAuth(owner))
	if w.Code != http.StatusBadRequest {
		t.Errorf("update with empty events must be 400, got %d %s", w.Code, body)
	}
}
