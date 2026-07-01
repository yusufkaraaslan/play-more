package models_test

import (
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// TestActiveWebhooksForEvent_ScopedToOwner is the regression test
// for the cross-tenant webhook leak: an event fired by one user
// must only ever match that user's own webhooks, never another
// user's subscription to the same event name.
func TestActiveWebhooksForEvent_ScopedToOwner(t *testing.T) {
	_ = testutil.NewTestServer(t) // sets storage.DB + AllowPrivateWebhookTargets

	a := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	b := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	if _, err := models.CreateWebhook(a.ID, "https://example.com/a", []string{models.WebhookEventGamePublished}); err != nil {
		t.Fatalf("CreateWebhook A: %v", err)
	}
	if _, err := models.CreateWebhook(b.ID, "https://example.com/b", []string{models.WebhookEventGamePublished}); err != nil {
		t.Fatalf("CreateWebhook B: %v", err)
	}

	hooksA, err := models.ActiveWebhooksForEvent(a.ID, models.WebhookEventGamePublished)
	if err != nil {
		t.Fatalf("lookup A: %v", err)
	}
	if len(hooksA) != 1 || hooksA[0].UserID != a.ID {
		t.Fatalf("owner A must get exactly its own webhook, got %d: %+v", len(hooksA), hooksA)
	}

	hooksB, err := models.ActiveWebhooksForEvent(b.ID, models.WebhookEventGamePublished)
	if err != nil {
		t.Fatalf("lookup B: %v", err)
	}
	if len(hooksB) != 1 || hooksB[0].UserID != b.ID {
		t.Fatalf("owner B must get exactly its own webhook, got %d: %+v", len(hooksB), hooksB)
	}
}

// TestValidateWebhookURL_SSRF verifies the SSRF guard: http(s)
// only, and no loopback / private / link-local targets. Uses IP
// literals so net.LookupIP resolves without any real DNS lookup.
func TestValidateWebhookURL_SSRF(t *testing.T) {
	// This test needs the guard ON; other tests (and testutil) turn
	// it off. Save/restore around the test.
	prev := models.AllowPrivateWebhookTargets
	models.AllowPrivateWebhookTargets = false
	t.Cleanup(func() { models.AllowPrivateWebhookTargets = prev })

	blocked := []string{
		"ftp://example.com/x",      // wrong scheme
		"http://127.0.0.1/hook",    // loopback
		"http://169.254.169.254/",  // link-local (cloud metadata)
		"http://10.0.0.5/hook",     // private
		"http://192.168.1.20/hook", // private
		"https://[::1]/hook",       // IPv6 loopback
		"not-a-url",                // unparseable / no scheme
	}
	for _, u := range blocked {
		if err := models.ValidateWebhookURL(u); err == nil {
			t.Errorf("expected %q to be rejected, got nil", u)
		}
	}

	// A public IP literal passes (no DNS needed).
	if err := models.ValidateWebhookURL("https://8.8.8.8/hook"); err != nil {
		t.Errorf("public target should pass, got %v", err)
	}

	// With the guard off, a loopback target is allowed (test bypass).
	models.AllowPrivateWebhookTargets = true
	if err := models.ValidateWebhookURL("http://127.0.0.1:8080/hook"); err != nil {
		t.Errorf("loopback should pass with guard off, got %v", err)
	}
}
