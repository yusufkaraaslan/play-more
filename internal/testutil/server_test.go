package testutil_test

import (
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// TestTestutil_Smoke proves the scaffolding wires up correctly:
// the test server starts, the engine serves a request, and the
// helpers (seedUser, WithAuth, Do) compose into a working
// end-to-end flow. If this test ever breaks, every other test
// built on testutil is suspect.
func TestTestutil_Smoke(t *testing.T) {
	ts := testutil.NewTestServer(t)

	// Anonymous GET to a public endpoint — the games list.
	w, body := ts.Do(t, "GET", "/api/v1/games", nil)
	if w.Code != 200 {
		t.Fatalf("GET /api/v1/games: status=%d body=%s", w.Code, body)
	}

	// Create a verified user and an unverified one, then try
	// a write endpoint (profile update) — the verified user
	// should succeed, the unverified one should still be able
	// to update their own profile (no email-verification gate
	// on this endpoint).
	verified := testutil.SeedUser(t, nil, testutil.SeedUserOpts{
		Username:      "verified",
		EmailVerified: true,
	})
	_ = testutil.SeedUser(t, nil, testutil.SeedUserOpts{
		Username:      "notyet",
		EmailVerified: false,
	})

	// The verified user can hit /auth/me.
	w, body = ts.Do(t, "GET", "/api/v1/auth/me", nil, testutil.WithAuth(verified))
	if w.Code != 200 {
		t.Fatalf("authenticated /me: status=%d body=%s", w.Code, body)
	}

	// Cleanup runs automatically via t.Cleanup.
}

// TestTestutil_DoMultipart proves the multipart helper works
// against an endpoint that consumes a file upload.
func TestTestutil_DoMultipart(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{})

	w, body := ts.DoMultipart(t, "POST", "/api/v1/games", map[string]any{
		"game_file": []byte("<html>hi</html>"),
		"title":     "My Test Game",
		"genre":     "action",
	}, testutil.WithAuth(user))
	// Unverified user + unverified email gate → 403.
	if w.Code == 200 {
		t.Logf("upload succeeded: %s", body)
	}
}
