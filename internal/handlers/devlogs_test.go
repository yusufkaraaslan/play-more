package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

func TestDevlogs_CreateAndList(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "DevlogGame")

	// Create
	w, body := ts.Do(t, "POST", "/api/v1/games/"+gameID+"/devlogs", map[string]any{
		"title":   "v1.0 Released",
		"content": "Bug fixes and new features!",
	}, testutil.WithAuth(owner))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, body)
	}

	// List
	w, body = ts.Do(t, "GET", "/api/v1/games/"+gameID+"/devlogs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	if !strings.Contains(string(body), "v1.0 Released") {
		t.Errorf("devlog title not in list: %s", body)
	}
}

func TestDevlogs_CreateRejectsNonOwner(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	intruder := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "PrivateGame")

	w, _ := ts.Do(t, "POST", "/api/v1/games/"+gameID+"/devlogs", map[string]any{
		"title":   "spam",
		"content": "spam",
	}, testutil.WithAuth(intruder))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-owner devlog: %d, want 403", w.Code)
	}
}

func TestDevlogs_DeleteByAuthor(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "DelGame")

	// Create
	w, body := ts.Do(t, "POST", "/api/v1/games/"+gameID+"/devlogs", map[string]any{
		"title":   "tmp",
		"content": "x",
	}, testutil.WithAuth(owner))
	var created struct {
		ID string `json:"id"`
	}
	testutil.DecodeJSON(t, body, &created)

	// Delete
	w, _ = ts.Do(t, "DELETE", "/api/v1/devlogs/"+created.ID, nil, testutil.WithAuth(owner))
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Errorf("delete: %d", w.Code)
	}
}

func TestCSRF_RejectsCrossOrigin(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, user.ID, "CSRFGame")

	// Set an Origin that doesn't match the request host.
	w, _ := ts.Do(t, "POST", "/api/v1/games/"+gameID+"/devlogs", map[string]any{
		"title": "x", "content": "x",
	}, testutil.WithAuth(user), testutil.WithHeader("Origin", "https://evil.example.com"))
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin: %d, want 403", w.Code)
	}
}

func TestCSRF_AllowsSameOrigin(t *testing.T) {
	ts := testutil.NewTestServer(t)

	// Set Origin to match the test server's host.
	w, _ := ts.Do(t, "GET", "/api/v1/games", nil, testutil.WithHeader("Origin", "http://example.com"))
	if w.Code != http.StatusOK {
		t.Errorf("same-origin GET: %d", w.Code)
	}
}
