package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// pointGameAtBuild makes the game's active entry the build's index.html —
// the shape every game uploaded through the builds pipeline has.
func pointGameAtBuild(t *testing.T, gameID, buildID string) {
	t.Helper()
	entry := filepath.ToSlash(filepath.Join("builds", buildID, "index.html"))
	if _, err := storage.DB.Exec(
		`UPDATE games SET entry_file = ?, file_path = ? WHERE id = ?`,
		entry, storage.BuildDir(gameID, buildID), gameID,
	); err != nil {
		t.Fatalf("point game at build: %v", err)
	}
}

// Regression: a build-based game has entry_file = builds/<id>/index.html, so
// the SPA sets frame.src to /play/<game>/builds/<id>/index.html. Go's
// http.ServeFile canonicalises any URL ending in "/index.html" with a 301 to
// the directory form — so "GET <build dir>/" is the request that actually has
// to serve the game.
//
// #87's directory-listing guard 404'd every directory unconditionally, which
// took every build-based game offline the moment it was deployed.
func TestServe_BuildDirectoryResolvesToIndex(t *testing.T) {
	ts := setupServeRoutes(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Dir Index "+uuid.NewString()[:8])
	buildID := "build_" + uuid.NewString()
	writeBuildFiles(t, gameID, buildID, "<html>GAME OK</html>")
	pointGameAtBuild(t, gameID, buildID)

	base := "/play/" + gameID + "/builds/" + buildID

	// The explicit index.html form is canonicalised by http.ServeFile.
	w, _ := ts.Do(t, "GET", base+"/index.html", nil)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("GET %s/index.html: got %d, want 301", base, w.Code)
	}

	// ...and the redirect target must serve the game, not 404.
	w, body := ts.Do(t, "GET", base+"/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s/: got %d, want 200 (this is the production outage)", base, w.Code)
	}
	if !strings.Contains(string(body), "GAME OK") {
		t.Fatalf("GET %s/: body did not contain the game's index.html: %q", base, string(body))
	}
}

// The guard's actual purpose must survive: a directory with no index.html is
// still a 404, never an auto-generated listing of the build's contents.
func TestServe_DirectoryWithoutIndexStillNotFound(t *testing.T) {
	ts := setupServeRoutes(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "No Index "+uuid.NewString()[:8])
	buildID := "build_" + uuid.NewString()
	writeBuildFiles(t, gameID, buildID, "<html>GAME OK</html>")
	pointGameAtBuild(t, gameID, buildID)

	// A subdirectory holding only an asset — no index.html.
	assets := filepath.Join(storage.BuildDir(gameID, buildID), "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assets, "sprite.png"), []byte("PNG"), 0o644); err != nil {
		t.Fatalf("write sprite: %v", err)
	}

	w, body := ts.Do(t, "GET", "/play/"+gameID+"/builds/"+buildID+"/assets/", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET assets/: got %d, want 404 — directory listings must stay blocked", w.Code)
	}
	if strings.Contains(string(body), "sprite.png") {
		t.Fatalf("GET assets/: response leaked a directory listing: %q", string(body))
	}
}
