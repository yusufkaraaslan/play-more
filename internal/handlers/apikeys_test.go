package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

func TestAPIKeys_CreateListRevoke(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	// Create
	w, body := ts.Do(t, "POST", "/api/v1/api-keys", map[string]any{
		"name": "CI deploy key",
	}, testutil.WithAuth(user))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, body)
	}
	var created struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		RawKey string `json:"raw_key"`
	}
	testutil.DecodeJSON(t, body, &created)
	if !strings.HasPrefix(created.RawKey, "pm_k_") {
		t.Errorf("raw_key prefix: %q", created.RawKey)
	}
	if created.Key.ID == "" {
		t.Error("key id is empty")
	}

	// List — should return the masked key (no plaintext)
	w, body = ts.Do(t, "GET", "/api/v1/api-keys", nil, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	if strings.Contains(string(body), created.RawKey) {
		t.Errorf("list leaked the raw key: %s", body)
	}

	// Revoke
	w, _ = ts.Do(t, "DELETE", "/api/v1/api-keys/"+created.Key.ID, nil, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Errorf("delete: %d", w.Code)
	}

	// Second delete: 404
	w, _ = ts.Do(t, "DELETE", "/api/v1/api-keys/"+created.Key.ID, nil, testutil.WithAuth(user))
	if w.Code != http.StatusNotFound {
		t.Errorf("second delete: %d, want 404", w.Code)
	}
}

func TestAPIKeys_RejectsUnverifiedEmail(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: false})

	w, _ := ts.Do(t, "POST", "/api/v1/api-keys", map[string]any{
		"name": "should fail",
	}, testutil.WithAuth(user))
	if w.Code != http.StatusForbidden {
		t.Errorf("unverified key create: %d, want 403", w.Code)
	}
}

func TestAPIKeys_AuthViaBearerToken(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	_, bearerOpt := testutil.WithAPIKey(user)

	// The Bearer key can hit /auth/me.
	w, body := ts.Do(t, "GET", "/api/v1/auth/me", nil, bearerOpt)
	if w.Code != http.StatusOK {
		t.Fatalf("bearer auth/me: %d %s", w.Code, body)
	}
}

func TestAPIKeys_RejectsInvalidToken(t *testing.T) {
	ts := testutil.NewTestServer(t)
	w, _ := ts.Do(t, "GET", "/api/v1/auth/me", nil, testutil.WithHeader("Authorization", "Bearer pm_k_invalid"))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("invalid bearer: %d, want 401", w.Code)
	}
}
