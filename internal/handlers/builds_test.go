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

func TestBuilds_GetOne(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "GetOneGame")
	buildID := testutil.SeedBuild(t, nil, gameID, owner.ID, "beta")

	w, body := ts.DoAuthed(t, "GET", "/api/v1/games/"+gameID+"/builds/"+buildID, nil, owner)
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, body)
	}
	if !strings.Contains(string(body), buildID) {
		t.Errorf("build_id missing: %s", body)
	}
}

func TestBuilds_DeleteRefusesActive(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "DeleteActiveGame")

	// The backfill migration created one active stable build.
	// Find it via list.
	w, body := ts.DoAuthed(t, "GET", "/api/v1/games/"+gameID+"/builds", nil, owner)
	if w.Code != 200 {
		t.Fatalf("list: %d", w.Code)
	}
	var listed struct {
		Builds []struct {
			ID       string `json:"id"`
			IsActive bool   `json:"is_active"`
		} `json:"builds"`
	}
	testutil.DecodeJSON(t, body, &listed)
	if len(listed.Builds) == 0 {
		t.Fatal("no builds seeded")
	}
	var activeID string
	for _, b := range listed.Builds {
		if b.IsActive {
			activeID = b.ID
			break
		}
	}
	if activeID == "" {
		t.Fatal("no active build found")
	}

	// Try to delete the active build — should 400.
	w, _ = ts.DoAuthed(t, "DELETE", "/api/v1/games/"+gameID+"/builds/"+activeID, nil, owner)
	if w.Code != 400 {
		t.Errorf("delete active: %d, want 400", w.Code)
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

// TestBuilds_Rollback seeds two stable builds, activates the newer
// one, then rolls back to the older one. The previous build must
// become the active build for the channel.
func TestBuilds_Rollback(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "RollbackGame")

	// The backfill migration created build #1 (active stable).
	// Seed a second stable build (#2, inactive).
	build2 := testutil.SeedBuild(t, nil, gameID, owner.ID, "stable")

	// Activate build #2 — now #2 is active, #1 is inactive.
	w, body := ts.DoAuthed(t, "PUT", "/api/v1/games/"+gameID+"/builds/"+build2+"/activate", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("activate build #2: %d %s", w.Code, body)
	}

	// Rollback from build #2 → should activate build #1.
	w, body = ts.DoAuthed(t, "POST", "/api/v1/games/"+gameID+"/builds/"+build2+"/rollback", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("rollback: %d %s", w.Code, body)
	}

	// List builds — build #1 (the backfill) should now be active.
	w, body = ts.DoAuthed(t, "GET", "/api/v1/games/"+gameID+"/builds", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, body)
	}
	var listed struct {
		Builds []struct {
			ID       string `json:"id"`
			IsActive bool   `json:"is_active"`
			Channel  string `json:"channel"`
		} `json:"builds"`
	}
	testutil.DecodeJSON(t, body, &listed)
	activeCount := 0
	for _, b := range listed.Builds {
		if b.Channel == "stable" && b.IsActive {
			activeCount++
			if b.ID == build2 {
				t.Errorf("build #2 should be inactive after rollback")
			}
		}
	}
	if activeCount != 1 {
		t.Errorf("expected exactly 1 active stable build, got %d", activeCount)
	}
}
