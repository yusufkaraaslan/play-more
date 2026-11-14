package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

func demoFileStorage(t *testing.T, dir string) {
	t.Helper()
	previous := storage.GamesDir
	if err := storage.InitFileStorage(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.GamesDir = previous })
}

type demoInstallResult struct {
	Installed []struct{ Key, ID, Title string }
	Skipped   []struct{ Key, ID, Title string }
}

func TestInstallDemos_PreservesCatalogAndIsRepeatable(t *testing.T) {
	ts := testutil.NewTestServer(t)
	demoFileStorage(t, ts.DataDir)
	admin := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{})
	existing := testutil.SeedGame(t, nil, other.ID, "My existing game")
	// An older seed or even an unrelated game with a matching title is never adopted or overwritten.
	legacy := testutil.SeedGame(t, nil, other.ID, "Co-op Canvas")
	if err := storage.SaveGameFile(legacy, "index.html", []byte("custom version")); err != nil {
		t.Fatal(err)
	}
	w, body := ts.DoAuthed(t, "POST", "/api/v1/admin/demo-games", nil, admin)
	if w.Code != 200 {
		t.Fatalf("install: %d %s", w.Code, body)
	}
	var result demoInstallResult
	testutil.DecodeJSON(t, body, &result)
	if len(result.Installed) != 4 || len(result.Skipped) != 1 {
		t.Fatalf("unexpected install: %s", body)
	}
	for _, d := range result.Installed {
		game, err := models.GetGameByID(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if game.DeveloperID != admin.ID || !game.Published || !models.StoreReadiness(game).Ready {
			t.Fatalf("incomplete demo: %+v", game)
		}
		if _, err := os.Stat(filepath.Join(storage.GameDir(game.ID), "index.html")); err != nil {
			t.Fatal(err)
		}
	}
	edited := result.Installed[0].ID
	if _, err := storage.DB.Exec(`UPDATE games SET title='My edited demo', published=0 WHERE id=?`, edited); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveGameFile(edited, "index.html", []byte("edited demo")); err != nil {
		t.Fatal(err)
	}
	w, body = ts.DoAuthed(t, "POST", "/api/v1/admin/demo-games", nil, admin)
	if w.Code != 200 {
		t.Fatalf("repeat: %d %s", w.Code, body)
	}
	testutil.DecodeJSON(t, body, &result)
	if len(result.Installed) != 0 || len(result.Skipped) != 5 {
		t.Fatalf("repeat duplicated demos: %s", body)
	}
	for id, want := range map[string]string{legacy: "custom version", edited: "edited demo"} {
		got, err := os.ReadFile(filepath.Join(storage.GameDir(id), "index.html"))
		if err != nil || string(got) != want {
			t.Fatalf("modified existing file: %s %v", got, err)
		}
	}
	g, err := models.GetGameByID(edited)
	if err != nil || g.Title != "My edited demo" || g.Published {
		t.Fatalf("edited demo changed: %+v %v", g, err)
	}
	if _, err := models.GetGameByID(existing); err != nil {
		t.Fatal("existing game removed", err)
	}
	var owner string
	if err := storage.DB.QueryRow(`SELECT developer_id FROM games WHERE id=?`, legacy).Scan(&owner); err != nil || owner != other.ID {
		t.Fatal("legacy owner changed", err)
	}
	// Deleting an installed demo permits a later reinstallation.
	if err := g.Delete(); err != nil {
		t.Fatal(err)
	}
	w, body = ts.DoAuthed(t, "POST", "/api/v1/admin/demo-games", nil, admin)
	if w.Code != 200 {
		t.Fatalf("reinstall: %d %s", w.Code, body)
	}
	testutil.DecodeJSON(t, body, &result)
	if len(result.Installed) != 1 {
		t.Fatalf("reinstall: %s", body)
	}
}

func TestInstallDemos_RequiresAdminSession(t *testing.T) {
	ts := testutil.NewTestServer(t)
	admin := testutil.SeedUser(t, nil, testutil.SeedUserOpts{})
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{})
	_, key := testutil.WithAPIKey(admin)
	for name, options := range map[string][]testutil.ReqOption{"anonymous": nil, "member": {testutil.WithAuth(other)}, "admin API key": {key}} {
		t.Run(name, func(t *testing.T) {
			w, body := ts.Do(t, "POST", "/api/v1/admin/demo-games", nil, options...)
			if w.Code != 404 {
				t.Fatalf("expected 404, got %d %s", w.Code, body)
			}
		})
	}
	var count int
	storage.DB.QueryRow(`SELECT count(*) FROM games`).Scan(&count)
	if count != 0 {
		t.Fatal("unauthorized request created games")
	}
}

func TestInstallDemos_FailedAssetsLeaveNoPublishedOrTrackedDraft(t *testing.T) {
	ts := testutil.NewTestServer(t)
	demoFileStorage(t, ts.DataDir)
	admin := testutil.SeedUser(t, nil, testutil.SeedUserOpts{})
	// A file in place of the games directory makes asset creation fail on every OS.
	blocked := filepath.Join(ts.DataDir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	storage.GamesDir = blocked
	w, body := ts.DoAuthed(t, "POST", "/api/v1/admin/demo-games", nil, admin)
	if w.Code != 500 {
		t.Fatalf("expected asset failure: %d %s", w.Code, body)
	}
	var count int
	if err := storage.DB.QueryRow(`SELECT count(*) FROM games`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed install left games: %d %v", count, err)
	}
}

func TestSeedData_UsesDemoRegistryAndStillSkipsExistingCatalog(t *testing.T) {
	ts := testutil.NewTestServer(t)
	demoFileStorage(t, ts.DataDir)
	admin := testutil.SeedUser(t, nil, testutil.SeedUserOpts{})
	w, body := ts.DoAuthed(t, "POST", "/api/v1/seed", nil, admin)
	if w.Code != 200 {
		t.Fatalf("seed: %d %s", w.Code, body)
	}
	var demos int
	storage.DB.QueryRow(`SELECT count(*) FROM games WHERE demo_key IS NOT NULL`).Scan(&demos)
	if demos != 5 {
		t.Fatalf("tracked demos: %d", demos)
	}
	w, body = ts.DoAuthed(t, "POST", "/api/v1/admin/demo-games", nil, admin)
	if w.Code != 200 {
		t.Fatalf("install after seed: %d %s", w.Code, body)
	}
	var result demoInstallResult
	testutil.DecodeJSON(t, body, &result)
	if len(result.Installed) != 0 || len(result.Skipped) != 5 {
		t.Fatalf("seeded demos duplicated: %s", body)
	}
	w, body = ts.DoAuthed(t, "POST", "/api/v1/seed", nil, admin)
	if w.Code != 200 {
		t.Fatalf("repeat seed: %d %s", w.Code, body)
	}
	var out struct{ Message string }
	testutil.DecodeJSON(t, body, &out)
	if out.Message != "already seeded" {
		t.Fatalf("seed behavior changed: %s", body)
	}
}

func TestInstallDemos_ConcurrentRequestsInstallEachDemoOnce(t *testing.T) {
	ts := testutil.NewTestServer(t)
	demoFileStorage(t, ts.DataDir)
	admin := testutil.SeedUser(t, nil, testutil.SeedUserOpts{})
	token, err := models.CreateSession(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(ts.Engine)
	defer srv.Close()
	type outcome struct {
		result demoInstallResult
		err    error
		status int
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("POST", srv.URL+"/api/v1/admin/demo-games", nil)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			req.Header.Set("Origin", srv.URL)
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
			resp, err := srv.Client().Do(req)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			defer resp.Body.Close()
			var result demoInstallResult
			err = json.NewDecoder(resp.Body).Decode(&result)
			results <- outcome{result: result, err: err, status: resp.StatusCode}
		}()
	}
	wg.Wait()
	close(results)
	total := 0
	for out := range results {
		if out.err != nil || out.status != 200 {
			t.Fatalf("concurrent install: status=%d err=%v", out.status, out.err)
		}
		total += len(out.result.Installed)
	}
	if total != 5 {
		t.Fatalf("installed %d demos across two requests", total)
	}
	var count int
	storage.DB.QueryRow(`SELECT count(*) FROM games`).Scan(&count)
	if count != 5 {
		t.Fatalf("duplicate games: %d", count)
	}
	var developer bool
	if err := storage.DB.QueryRow(`SELECT is_developer FROM users WHERE id=?`, admin.ID).Scan(&developer); err != nil || !developer {
		t.Fatalf("owner not marked developer: %v", err)
	}
}
