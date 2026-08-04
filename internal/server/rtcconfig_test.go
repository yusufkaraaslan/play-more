package server

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

// ctxWithUser builds a gin context carrying an authenticated user,
// which is what /rtc-config sees after AuthRequired has run.
func ctxWithUser(user *models.User) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rtc-config", nil)
	if user != nil {
		c.Set(middleware.UserKey, user)
	}
	return c
}

// withStatic swaps the package-level ICE config for one test and
// restores it afterwards.
func withStatic(t *testing.T, servers []map[string]any, fn func(userID string) []map[string]any) {
	t.Helper()
	prevServers, prevFunc := RTCIceServers, TURNCredentialFunc
	RTCIceServers, TURNCredentialFunc = servers, fn
	t.Cleanup(func() { RTCIceServers, TURNCredentialFunc = prevServers, prevFunc })
}

func TestICEServersFor_NoEmbeddedTURN(t *testing.T) {
	static := []map[string]any{{"urls": "stun:stun.l.google.com:19302"}}
	withStatic(t, static, nil)

	got := iceServersFor(ctxWithUser(&models.User{ID: "user-1"}))
	if len(got) != 1 {
		t.Fatalf("expected only the static entry, got %d", len(got))
	}
}

func TestICEServersFor_AppendsMintedCredentials(t *testing.T) {
	static := []map[string]any{{"urls": "stun:stun.l.google.com:19302"}}
	withStatic(t, static, func(userID string) []map[string]any {
		return []map[string]any{{"urls": "turn:203.0.113.10:3478", "username": userID}}
	})

	got := iceServersFor(ctxWithUser(&models.User{ID: "user-1"}))
	if len(got) != 2 {
		t.Fatalf("expected static + minted, got %d", len(got))
	}
	if got[1]["username"] != "user-1" {
		t.Errorf("minted entry should be scoped to the requesting user, got %v", got[1]["username"])
	}
}

// No authenticated user means nothing to mint against. AuthRequired
// makes this unreachable in production, but iceServersFor must not
// depend on that to avoid a nil deref.
func TestICEServersFor_NoUser(t *testing.T) {
	static := []map[string]any{{"urls": "stun:stun.l.google.com:19302"}}
	called := false
	withStatic(t, static, func(userID string) []map[string]any {
		called = true
		return []map[string]any{{"urls": "turn:203.0.113.10:3478"}}
	})

	got := iceServersFor(ctxWithUser(nil))
	if len(got) != 1 {
		t.Fatalf("expected only the static entry, got %d", len(got))
	}
	if called {
		t.Error("credential minting should not run without an authenticated user")
	}
}

// The regression this file exists for: appending straight onto the
// package-level RTCIceServers would write into its spare capacity, so
// two concurrent requests would overwrite each other's slot — handing
// one user another user's TURN credential.
//
// The static slice below deliberately has cap > len, which is the only
// condition under which that aliasing bug is observable.
func TestICEServersFor_DoesNotMutateStaticList(t *testing.T) {
	static := make([]map[string]any, 1, 4) // spare capacity on purpose
	static[0] = map[string]any{"urls": "stun:stun.l.google.com:19302"}

	withStatic(t, static, func(userID string) []map[string]any {
		return []map[string]any{{"urls": "turn:203.0.113.10:3478", "username": userID}}
	})

	first := iceServersFor(ctxWithUser(&models.User{ID: "user-1"}))
	second := iceServersFor(ctxWithUser(&models.User{ID: "user-2"}))

	// Each caller must see only its own credential.
	if first[1]["username"] != "user-1" {
		t.Errorf("first caller got %v, want user-1", first[1]["username"])
	}
	if second[1]["username"] != "user-2" {
		t.Errorf("second caller got %v, want user-2", second[1]["username"])
	}

	// And the shared slice must be untouched — same length, and its
	// spare capacity must not have been written through.
	if len(RTCIceServers) != 1 {
		t.Fatalf("static list grew to %d entries", len(RTCIceServers))
	}
	if grown := RTCIceServers[:cap(RTCIceServers)]; grown[1] != nil {
		t.Errorf("static list's spare capacity was written through: %v", grown[1])
	}
}
