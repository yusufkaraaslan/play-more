package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

func TestWebhooks_CreateAndList(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	// Create
	w, body := ts.DoAuthed(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"game.published", "devlog.created"},
	}, user)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, body)
	}
	var created struct {
		ID     string   `json:"id"`
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret"`
	}
	testutil.DecodeJSON(t, body, &created)
	if created.ID == "" {
		t.Fatal("no id in response")
	}
	if created.Secret == "" {
		t.Fatal("no secret in response")
	}
	if len(created.Secret) != 64 {
		t.Errorf("secret should be 64 hex chars, got %d (%q)", len(created.Secret), created.Secret)
	}
	if len(created.Events) != 2 {
		t.Errorf("events: %v", created.Events)
	}

	// List — secret should be redacted.
	w, body = ts.DoAuthed(t, "GET", "/api/v1/webhooks", nil, user)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	if strings.Contains(string(body), created.Secret) {
		t.Errorf("list leaked the raw secret: %s", body)
	}
}

func TestWebhooks_CreateRejectsBadEvent(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	w, _ := ts.DoAuthed(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"not.a.real.event"},
	}, user)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad event: %d, want 400", w.Code)
	}
}

func TestWebhooks_CreateRejectsNonHTTPS(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	w, _ := ts.DoAuthed(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "ftp://example.com/hook",
		"events": []string{"game.published"},
	}, user)
	if w.Code != http.StatusBadRequest {
		t.Errorf("non-http: %d, want 400", w.Code)
	}
}

func TestWebhooks_RejectsAnonymous(t *testing.T) {
	ts := testutil.NewTestServer(t)
	w, _ := ts.Do(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"game.published"},
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", w.Code)
	}
}

func TestWebhooks_Delete(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	// Create
	w, body := ts.DoAuthed(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"game.published"},
	}, user)
	var created struct {
		ID string `json:"id"`
	}
	testutil.DecodeJSON(t, body, &created)

	// Delete
	w, _ = ts.DoAuthed(t, "DELETE", "/api/v1/webhooks/"+created.ID, nil, user)
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Errorf("delete: %d", w.Code)
	}

	// Second delete: 404
	w, _ = ts.DoAuthed(t, "DELETE", "/api/v1/webhooks/"+created.ID, nil, user)
	if w.Code != http.StatusNotFound {
		t.Errorf("second delete: %d, want 404", w.Code)
	}
}

// TestWebhooks_GetOne creates a webhook then fetches it by ID.
// Also verifies cross-tenant isolation: another user's GET returns 404.
func TestWebhooks_GetOne(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	// Create
	w, body := ts.DoAuthed(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"game.published"},
	}, user)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, body)
	}
	var created struct {
		ID     string   `json:"id"`
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	testutil.DecodeJSON(t, body, &created)
	if created.ID == "" {
		t.Fatal("no id")
	}

	// GET by owner — 200 with fields.
	w, body = ts.DoAuthed(t, "GET", "/api/v1/webhooks/"+created.ID, nil, user)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, body)
	}
	var got struct {
		ID     string   `json:"id"`
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	testutil.DecodeJSON(t, body, &got)
	if got.ID != created.ID {
		t.Errorf("id mismatch: %q vs %q", got.ID, created.ID)
	}
	if got.URL != "https://example.com/hook" {
		t.Errorf("url mismatch: %q", got.URL)
	}
	if len(got.Events) != 1 || got.Events[0] != "game.published" {
		t.Errorf("events mismatch: %v", got.Events)
	}

	// GET by wrong user — 404 (cross-tenant isolation).
	w, _ = ts.DoAuthed(t, "GET", "/api/v1/webhooks/"+created.ID, nil, other)
	if w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant get: %d, want 404", w.Code)
	}
}

// TestWebhooks_Update creates a webhook, changes its events + active
// flag, then GETs back to verify the diff.
func TestWebhooks_Update(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	// Create with one event.
	w, body := ts.DoAuthed(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"game.published"},
	}, user)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	testutil.DecodeJSON(t, body, &created)

	// Update — change events + deactivate.
	w, body = ts.DoAuthed(t, "PUT", "/api/v1/webhooks/"+created.ID, map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"game.published", "devlog.created"},
		"active": false,
	}, user)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, body)
	}

	// GET back and verify the diff.
	w, body = ts.DoAuthed(t, "GET", "/api/v1/webhooks/"+created.ID, nil, user)
	if w.Code != http.StatusOK {
		t.Fatalf("get after update: %d %s", w.Code, body)
	}
	var got struct {
		Events []string `json:"events"`
		Active bool     `json:"active"`
	}
	testutil.DecodeJSON(t, body, &got)
	if len(got.Events) != 2 {
		t.Errorf("events after update: %v, want 2 items", got.Events)
	}
	if got.Active {
		t.Errorf("active after update: true, want false")
	}
}

// TestWebhooks_ListDeliveries_Empty creates a webhook and fetches
// its deliveries — should be an empty list (200).
func TestWebhooks_ListDeliveries_Empty(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	w, body := ts.DoAuthed(t, "POST", "/api/v1/webhooks", map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"game.published"},
	}, user)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	testutil.DecodeJSON(t, body, &created)

	w, body = ts.DoAuthed(t, "GET", "/api/v1/webhooks/"+created.ID+"/deliveries", nil, user)
	if w.Code != http.StatusOK {
		t.Fatalf("list deliveries: %d %s", w.Code, body)
	}
	var resp struct {
		Deliveries []any `json:"deliveries"`
	}
	testutil.DecodeJSON(t, body, &resp)
	if len(resp.Deliveries) != 0 {
		t.Errorf("deliveries: %d, want 0", len(resp.Deliveries))
	}
}
