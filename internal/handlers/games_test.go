package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

func TestGames_ListAnonymous(t *testing.T) {
	ts := testutil.NewTestServer(t)
	w, _ := ts.Do(t, "GET", "/api/v1/games", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
}

func TestGames_CreateRequiresVerifiedEmail(t *testing.T) {
	ts := testutil.NewTestServer(t)
	unverified := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: false})

	w, body := ts.Do(t, "POST", "/api/v1/games", nil, testutil.WithAuth(unverified))
	// Unverified user should be rejected by the email gate.
	if w.Code != http.StatusForbidden {
		t.Errorf("unverified upload: got %d, want 403\nbody: %s", w.Code, body)
	}
}

func TestGames_CreateRejectsAnonymous(t *testing.T) {
	ts := testutil.NewTestServer(t)
	w, _ := ts.Do(t, "POST", "/api/v1/games", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: got %d, want 401", w.Code)
	}
}

func TestGames_CreateRejectsBadFileType(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	w, body := ts.DoMultipart(t, "POST", "/api/v1/games", map[string]any{
		"game_file": []byte("not actually a zip"),
		"title":     "My Game",
		"genre":     "action",
	}, testutil.WithAuth(user))
	// Should fail with 400 — bad archive.
	if w.Code == http.StatusOK {
		t.Errorf("expected non-200 for invalid zip, got %d\nbody: %s", w.Code, body)
	}
}

func TestGames_UpdateRejectsNonOwner(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	intruder := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Owner Game")

	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID, map[string]any{
		"title": "Pwned",
	}, testutil.WithAuth(intruder))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-owner PUT: got %d, want 403\nbody: %s", w.Code, body)
	}
	// Confirm the title wasn't actually changed.
	w, body = ts.Do(t, "GET", "/api/v1/games/"+gameID, nil)
	if !strings.Contains(string(body), "Owner Game") {
		t.Errorf("title was changed by intruder: %s", body)
	}
}

func TestGames_DeleteOwnerOnly(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Doomed Game")

	// Owner can delete.
	w, _ := ts.Do(t, "DELETE", "/api/v1/games/"+gameID, nil, testutil.WithAuth(owner))
	if w.Code != http.StatusOK {
		t.Errorf("owner DELETE: got %d", w.Code)
	}
	// 404 on second delete (cascade should also have removed the row).
	w, _ = ts.Do(t, "DELETE", "/api/v1/games/"+gameID, nil, testutil.WithAuth(owner))
	if w.Code != http.StatusNotFound {
		t.Errorf("second DELETE: got %d, want 404", w.Code)
	}

	// Non-owner can't delete (on a fresh game).
	gameID2 := testutil.SeedGame(t, nil, owner.ID, "Second Game")
	w, _ = ts.Do(t, "DELETE", "/api/v1/games/"+gameID2, nil, testutil.WithAuth(other))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-owner DELETE: got %d, want 403", w.Code)
	}
}

func TestGames_ToggleVisibility(t *testing.T) {
	ts := testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "ToggleMe")

	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/visibility", map[string]any{
		"published": false,
	}, testutil.WithAuth(owner))
	if w.Code != http.StatusOK {
		t.Fatalf("unpublish: %d %s", w.Code, body)
	}
	// Anonymous list should no longer see it.
	w, body = ts.Do(t, "GET", "/api/v1/games", nil)
	if strings.Contains(string(body), "ToggleMe") {
		t.Errorf("unpublished game still appears in list: %s", body)
	}
}
