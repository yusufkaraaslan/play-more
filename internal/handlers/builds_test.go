package handlers_test

import (
	"archive/zip"
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

func TestBuilds_ListAndGet(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "ListBuilds")

	// Initially: backfill migration created one stable build.
	w, body := ts.DoAuthed(t, "GET", "/api/v1/games/"+gameID+"/builds", nil, owner)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, body)
	}
	if !strings.Contains(string(body), "stable") {
		t.Errorf("expected stable build in list: %s", body)
	}
}

func TestBuilds_ActivateRoundTrip(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "ActivateGame")
	// Seed a second stable build so we have something to activate.
	buildID := testutil.SeedBuild(t, nil, gameID, owner.ID, "stable")

	w, body := ts.DoAuthed(t, "PUT", "/api/v1/games/"+gameID+"/builds/"+buildID+"/activate", nil, owner)
	if w.Code != 200 {
		t.Fatalf("activate: %d %s", w.Code, body)
	}
}

// TestBuilds_ReuploadGoesThroughBuildChannels is the integration
// test for the reupload-via-build-channels refactor (#4 fix 3.12).
// A successful reupload should: create a new build row,
// promote it to active for the stable channel, update
// games.file_path + games.entry_file, and return the new
// build_id.
func TestBuilds_ReuploadGoesThroughBuildChannels(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "ReuploadGame")

	// Build a small zip in memory.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("index.html")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := fw.Write([]byte("<html>new</html>")); err != nil {
		t.Fatalf("write zip entry: %v", err)
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
	var resp struct {
		BuildID     string `json:"build_id"`
		BuildNumber int    `json:"build_number"`
	}
	testutil.DecodeJSON(t, body, &resp)
	if resp.BuildID == "" {
		t.Fatal("no build_id in response")
	}
	if resp.BuildNumber < 2 {
		t.Errorf("build_number should be >= 2 (initial + this one), got %d", resp.BuildNumber)
	}

	// A new active build should now exist for the stable channel.
	w, body = ts.Do(t, "GET", "/api/v1/games/"+gameID+"/builds", nil, testutil.WithAuth(owner))
	if w.Code != http.StatusOK {
		t.Fatalf("list builds: %d", w.Code)
	}
	if !strings.Contains(string(body), resp.BuildID) {
		t.Errorf("new build not in list: %s", body)
	}
}
