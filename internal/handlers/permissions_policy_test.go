package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// TestServe_GameHTMLDelegatesWebGPU verifies the Permissions-Policy
// response header on sandboxed game HTML:
//   - includes webgpu=* (so a sandboxed/opaque- or cross-origin game
//     iframe can call navigator.gpu — required for Godot-WebGPU exports),
//   - preserves the baseline restrictions (camera=(), microphone=(), …)
//     that the global securityHeaders middleware sets on every response,
//     because c.Header() overwrites rather than appends.
//
// The matching client-side delegation (iframe allow="webgpu") lives in
// frontend/index.html::launchGame; this test pins the server half.
func TestServe_GameHTMLDelegatesWebGPU(t *testing.T) {
	ts := testutil.NewTestServer(t)

	prevGames := storage.GamesDir
	storage.GamesDir = t.TempDir()
	t.Cleanup(func() { storage.GamesDir = prevGames })
	ts.Engine.GET("/play/:id", handlers.ServeGameFiles("", ""))
	ts.Engine.GET("/play/:id/*filepath", handlers.ServeGameFiles("", ""))

	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "WebGPU Header Game")

	dir := filepath.Join(storage.GamesDir, gameID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "game.js"), []byte("// asset"), 0o644); err != nil {
		t.Fatal(err)
	}

	// HTML: must carry webgpu=* AND the baseline restrictions, plus COOP.
	// Bare form (no /index.html) avoids http.ServeFile's index.html→dir
	// 301 normalization, matching the existing serve test pattern.
	w, _ := ts.Do(t, "GET", "/play/"+gameID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("serve html: %d", w.Code)
	}
	pp := w.Header().Get("Permissions-Policy")
	if !strings.Contains(pp, "webgpu=*") {
		t.Errorf("html: Permissions-Policy must delegate webgpu, got: %q", pp)
	}
	for _, want := range []string{"camera=()", "microphone=()", "geolocation=()", "gyroscope=()", "usb=()"} {
		if !strings.Contains(pp, want) {
			t.Errorf("html: Permissions-Policy must preserve baseline %s (c.Header overwrites the global middleware), got: %q", want, pp)
		}
	}
	if got := w.Header().Get("Cross-Origin-Opener-Policy"); got != "same-origin" {
		t.Errorf("html: COOP must be same-origin, got: %q", got)
	}

	// Non-HTML asset: handler must NOT set the HTML-only Permissions-Policy
	// (the global securityHeaders middleware would still set the baseline
	// in production, but ServeGameFiles itself only emits the webgpu=*
	// delegation on sandboxed document responses).
	w, _ = ts.Do(t, "GET", "/play/"+gameID+"/game.js", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("serve js: %d", w.Code)
	}
	if got := w.Header().Get("Permissions-Policy"); strings.Contains(got, "webgpu") {
		t.Errorf("js: Permissions-Policy must not delegate webgpu on non-HTML assets, got: %q", got)
	}
	if got := w.Header().Get("Cross-Origin-Opener-Policy"); got == "same-origin" {
		t.Errorf("js: COOP must not be set on non-HTML assets, got: %q", got)
	}
}
