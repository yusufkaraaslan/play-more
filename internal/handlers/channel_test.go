package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// setupServeRoutes mounts /play routes on the test server, using a temp
// games dir so file operations are isolated. Returns a cleanup function.
func setupServeRoutes(t *testing.T) *testutil.TestServer {
	t.Helper()
	ts := testutil.NewTestServer(t)
	prevGames := storage.GamesDir
	storage.GamesDir = t.TempDir()
	t.Cleanup(func() { storage.GamesDir = prevGames })
	ts.Engine.GET("/play/:id", handlers.ServeGameFiles("", ""))
	ts.Engine.GET("/play/:id/*filepath", handlers.ServeGameFiles("", ""))
	return ts
}

// writeBuildFiles writes an index.html with the given marker into the
// build directory on disk and returns the file_path.
func writeBuildFiles(t *testing.T, gameID, buildID, marker string) string {
	t.Helper()
	dir := storage.BuildDir(gameID, buildID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(marker), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	return filepath.ToSlash(filepath.Join("builds", buildID, "index.html"))
}

func TestServe_OwnerCanPreviewBeta(t *testing.T) {
	ts := setupServeRoutes(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Beta Preview")

	// Also write the stable build so the default path works.
	stableDir := filepath.Join(storage.GamesDir, gameID)
	os.MkdirAll(stableDir, 0o755)
	os.WriteFile(filepath.Join(stableDir, "index.html"), []byte("STABLE"), 0o644)

	// Create an active beta build.
	betaBuildID := "build_beta_" + gameID[:8]
	betaEntry := writeBuildFiles(t, gameID, betaBuildID, "BETA_MARKER")
	_, err := storage.DB.Exec(
		`INSERT INTO game_builds (id, game_id, build_number, channel, file_path, entry_file, size, is_active, created_by)
		 VALUES (?, ?, 2, 'beta', ?, ?, 100, 1, ?)`,
		betaBuildID, gameID, betaEntry, betaEntry, owner.ID)
	if err != nil {
		t.Fatalf("seed beta build: %v", err)
	}

	// Owner can preview the beta build.
	w, body := ts.DoAuthed(t, "GET", "/play/"+gameID+"?channel=beta", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("owner beta preview: %d %s", w.Code, body)
	}
	if string(body) != "BETA_MARKER" {
		t.Errorf("beta content mismatch: got %q, want BETA_MARKER", body)
	}

	// Non-owner gets 404.
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	w, _ = ts.DoAuthed(t, "GET", "/play/"+gameID+"?channel=beta", nil, other)
	if w.Code != http.StatusNotFound {
		t.Errorf("non-owner beta preview: got %d, want 404", w.Code)
	}

	// Anonymous gets 404.
	w, _ = ts.Do(t, "GET", "/play/"+gameID+"?channel=beta", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("anonymous beta preview: got %d, want 404", w.Code)
	}
}

func TestServe_OwnerCanPreviewInternal(t *testing.T) {
	ts := setupServeRoutes(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Internal Preview")

	stableDir := filepath.Join(storage.GamesDir, gameID)
	os.MkdirAll(stableDir, 0o755)
	os.WriteFile(filepath.Join(stableDir, "index.html"), []byte("STABLE"), 0o644)

	internalBuildID := "build_int_" + gameID[:8]
	internalEntry := writeBuildFiles(t, gameID, internalBuildID, "INTERNAL_MARKER")
	_, err := storage.DB.Exec(
		`INSERT INTO game_builds (id, game_id, build_number, channel, file_path, entry_file, size, is_active, created_by)
		 VALUES (?, ?, 2, 'internal', ?, ?, 100, 1, ?)`,
		internalBuildID, gameID, internalEntry, internalEntry, owner.ID)
	if err != nil {
		t.Fatalf("seed internal build: %v", err)
	}

	w, body := ts.DoAuthed(t, "GET", "/play/"+gameID+"?channel=internal", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("owner internal preview: %d %s", w.Code, body)
	}
	if string(body) != "INTERNAL_MARKER" {
		t.Errorf("internal content mismatch: got %q, want INTERNAL_MARKER", body)
	}

	// Non-owner gets 404.
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	w, _ = ts.DoAuthed(t, "GET", "/play/"+gameID+"?channel=internal", nil, other)
	if w.Code != http.StatusNotFound {
		t.Errorf("non-owner internal preview: got %d, want 404", w.Code)
	}
}

func TestServe_InvalidChannelReturns400(t *testing.T) {
	ts := setupServeRoutes(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Invalid Channel")

	stableDir := filepath.Join(storage.GamesDir, gameID)
	os.MkdirAll(stableDir, 0o755)
	os.WriteFile(filepath.Join(stableDir, "index.html"), []byte("STABLE"), 0o644)

	w, _ := ts.DoAuthed(t, "GET", "/play/"+gameID+"?channel=foo", nil, owner)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid channel: got %d, want 400", w.Code)
	}
}

func TestServe_BetaChannelNoActiveBuildReturns404(t *testing.T) {
	ts := setupServeRoutes(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "No Beta Build")

	stableDir := filepath.Join(storage.GamesDir, gameID)
	os.MkdirAll(stableDir, 0o755)
	os.WriteFile(filepath.Join(stableDir, "index.html"), []byte("STABLE"), 0o644)

	// No beta build exists — owner gets 404.
	w, _ := ts.DoAuthed(t, "GET", "/play/"+gameID+"?channel=beta", nil, owner)
	if w.Code != http.StatusNotFound {
		t.Errorf("no beta build: got %d, want 404", w.Code)
	}
}
