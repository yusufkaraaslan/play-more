package handlers_test

import (
	"net/http"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// completeStorePage fills in everything the gate asks for.
func completeStorePage(t *testing.T, gameID string) {
	t.Helper()
	_, err := storage.DB.Exec(
		`UPDATE games SET description = ?, cover_path = ?, screenshots = ?, tags = ? WHERE id = ?`,
		"A long enough description of this game to clear the eighty character bar comfortably.",
		"/play/"+gameID+"/cover.png",
		`["/play/`+gameID+`/shot1.png"]`,
		`["Action","Arcade","Singleplayer"]`,
		gameID,
	)
	if err != nil {
		t.Fatalf("complete store page: %v", err)
	}
}

func setVisibility(t *testing.T, ts *testutil.TestServer, gameID string, user *models.User, published bool) (*http.Response, []byte) {
	t.Helper()
	w, body := ts.DoAuthed(t, "PUT", "/api/v1/games/"+gameID+"/visibility",
		map[string]bool{"published": published}, user)
	return w.Result(), body
}

// A game with an empty store page must not be publishable, and the rejection
// has to say what is outstanding — a bare refusal leaves the developer guessing.
func TestPublish_BlockedWhenStorePageIncomplete(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Incomplete Game")
	// SeedGame inserts published=1; force the realistic starting state so the
	// post-condition below actually proves the gate blocked the transition
	// rather than reading back the seeded value.
	if _, err := storage.DB.Exec(`UPDATE games SET published = 0 WHERE id = ?`, gameID); err != nil {
		t.Fatal(err)
	}

	res, body := setVisibility(t, ts, gameID, owner, true)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("publishing an empty store page: got %d, want 422. body=%s", res.StatusCode, body)
	}

	var out struct {
		Error   string   `json:"error"`
		Missing []string `json:"missing"`
		Items   []struct {
			Key  string `json:"key"`
			Done bool   `json:"done"`
			Hint string `json:"hint"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, body, &out)
	if len(out.Missing) != 4 {
		t.Errorf("expected all 4 requirements outstanding, got %v", out.Missing)
	}
	for _, it := range out.Items {
		if !it.Done && it.Hint == "" {
			t.Errorf("unmet item %q has no hint — the developer needs to know what to do", it.Key)
		}
	}

	// And it must genuinely still be a draft, not merely reported as rejected.
	var published bool
	storage.DB.QueryRow(`SELECT published FROM games WHERE id = ?`, gameID).Scan(&published)
	if published {
		t.Error("game was published despite the gate rejecting the request")
	}
}

func TestPublish_AllowedWhenStorePageComplete(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Complete Game")
	if _, err := storage.DB.Exec(`UPDATE games SET published = 0 WHERE id = ?`, gameID); err != nil {
		t.Fatal(err)
	}
	completeStorePage(t, gameID)

	res, body := setVisibility(t, ts, gameID, owner, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("publishing a complete store page: got %d, want 200. body=%s", res.StatusCode, body)
	}
	var published bool
	storage.DB.QueryRow(`SELECT published FROM games WHERE id = ?`, gameID).Scan(&published)
	if !published {
		t.Error("game not actually published")
	}
}

// Pulling your own game offline must never be conditional on the store page
// being complete — that would trap exactly the games most in need of pulling.
func TestUnpublish_IsNeverGated(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Pull Me Offline")

	res, body := setVisibility(t, ts, gameID, owner, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unpublishing an incomplete game: got %d, want 200. body=%s", res.StatusCode, body)
	}
	var published bool
	storage.DB.QueryRow(`SELECT published FROM games WHERE id = ?`, gameID).Scan(&published)
	if published {
		t.Error("game still published after unpublish")
	}
}

// Games that were already live when the gate landed keep their exemption, so
// a developer who temporarily pulls one can always put it back.
func TestPublish_GrandfatheredGameBypassesGate(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Grandfathered Game")
	if _, err := storage.DB.Exec(`UPDATE games SET store_gate_exempt = 1 WHERE id = ?`, gameID); err != nil {
		t.Fatal(err)
	}

	// Unpublish, then republish — the round trip a developer makes when fixing
	// something. An exempt game must survive it despite an empty store page.
	if res, body := setVisibility(t, ts, gameID, owner, false); res.StatusCode != http.StatusOK {
		t.Fatalf("unpublish: got %d. body=%s", res.StatusCode, body)
	}
	res, body := setVisibility(t, ts, gameID, owner, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("republishing a grandfathered game: got %d, want 200. body=%s", res.StatusCode, body)
	}
}

// Uploading is not publishing.
func TestCreateGame_StartsAsDraft(t *testing.T) {
	testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	game, err := models.CreateGame("Fresh Upload", "action", "", owner.ID, 0, nil, false, false)
	if err != nil {
		t.Fatalf("create game: %v", err)
	}
	if game.Published {
		t.Error("returned Game reports Published — uploading must yield a draft")
	}
	var published, exempt bool
	storage.DB.QueryRow(`SELECT published, store_gate_exempt FROM games WHERE id = ?`, game.ID).
		Scan(&published, &exempt)
	if published {
		t.Error("new game is published in the DB — uploading must yield a draft")
	}
	if exempt {
		t.Error("new game is gate-exempt — only games predating the gate may be")
	}
}

func TestReadinessEndpoint(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Readiness Game")

	type readiness struct {
		Ready      bool     `json:"ready"`
		Exempt     bool     `json:"exempt"`
		CanPublish bool     `json:"can_publish"`
		Missing    []string `json:"missing"`
	}

	w, body := ts.DoAuthed(t, "GET", "/api/v1/games/"+gameID+"/readiness", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("owner readiness: %d %s", w.Code, body)
	}
	var before readiness
	testutil.DecodeJSON(t, body, &before)
	if before.Ready || before.CanPublish {
		t.Errorf("empty store page reported as ready: %+v", before)
	}

	completeStorePage(t, gameID)
	w, body = ts.DoAuthed(t, "GET", "/api/v1/games/"+gameID+"/readiness", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("owner readiness after completion: %d %s", w.Code, body)
	}
	var after readiness
	testutil.DecodeJSON(t, body, &after)
	if !after.Ready || !after.CanPublish {
		t.Errorf("completed store page not reported ready: %+v", after)
	}
	if len(after.Missing) != 0 {
		t.Errorf("completed store page still lists missing items: %v", after.Missing)
	}

	// The checklist describes an unpublished page; nobody else may read it.
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	w, _ = ts.DoAuthed(t, "GET", "/api/v1/games/"+gameID+"/readiness", nil, other)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-owner readiness: got %d, want 403", w.Code)
	}
}

// The bar is measured in characters, not bytes: a description in a non-Latin
// script must not clear it on byte count alone.
func TestStoreReadiness_CountsRunesNotBytes(t *testing.T) {
	// 40 three-byte runes: 120 bytes, but only 40 characters.
	desc := ""
	for i := 0; i < 40; i++ {
		desc += "あ"
	}
	g := &models.Game{
		Description: desc,
		CoverPath:   "/cover.png",
		Screenshots: []string{"/a.png"},
		Tags:        []string{"a", "b", "c"},
	}
	if r := models.StoreReadiness(g); r.Ready {
		t.Errorf("40 characters (120 bytes) passed an 80-character bar: %+v", r.Missing)
	}

	for i := 0; i < 45; i++ {
		desc += "あ"
	}
	g.Description = desc
	if r := models.StoreReadiness(g); !r.Ready {
		t.Errorf("85 characters failed an 80-character bar: %v", r.Missing)
	}
}

// Grandfathering must happen exactly once, ever.
//
// Every statement in migrationsAll() re-runs on each boot, and a failing ALTER
// does not stop the ones after it. An unguarded `UPDATE ... WHERE published=1`
// therefore re-stamped on every restart, silently exempting each game as it
// was published and eroding the gate to nothing. This asserts the guard holds.
func TestGrandfathering_DoesNotReapplyOnRestart(t *testing.T) {
	testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Published After The Gate")

	// A game that went live legitimately, by passing the gate: published, and
	// pointedly NOT exempt.
	if _, err := storage.DB.Exec(
		`UPDATE games SET published = 1, store_gate_exempt = 0 WHERE id = ?`, gameID); err != nil {
		t.Fatal(err)
	}

	// Replay every migration, exactly as a restart does.
	for i := 0; i < 3; i++ {
		for _, m := range storage.Migrations() {
			if _, err := storage.DB.Exec(m); err != nil && !storage.IsIdempotentMigrationError(err) {
				t.Fatalf("replaying migration %q: %v", m, err)
			}
		}
	}

	var exempt bool
	if err := storage.DB.QueryRow(
		`SELECT store_gate_exempt FROM games WHERE id = ?`, gameID).Scan(&exempt); err != nil {
		t.Fatal(err)
	}
	if exempt {
		t.Error("restart re-ran the grandfathering UPDATE — a game published under the gate " +
			"became exempt, which would erode the gate to nothing over time")
	}
}
