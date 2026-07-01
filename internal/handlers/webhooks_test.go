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
