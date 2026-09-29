package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/handlers"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// Regression: a game reading localStorage at module-init time never started.
// /play serves game HTML under a `sandbox` CSP without allow-same-origin, so
// the localStorage *getter* throws SecurityError; the throw escapes the game's
// top-level IIFE and nothing after it — including the first
// requestAnimationFrame — ever runs. ServeGameFiles splices an in-memory
// Storage shim in ahead of the game's own scripts to defuse that.
func TestServe_GameHTMLCarriesStorageShim(t *testing.T) {
	ts := testutil.NewTestServer(t)

	prevGames := storage.GamesDir
	storage.GamesDir = t.TempDir()
	t.Cleanup(func() { storage.GamesDir = prevGames })
	ts.Engine.GET("/play/:id", handlers.ServeGameFiles("", ""))
	ts.Engine.GET("/play/:id/*filepath", handlers.ServeGameFiles("", ""))

	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Storage Shim Game")

	dir := filepath.Join(storage.GamesDir, gameID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mirrors the shape of the game that surfaced this: an unguarded read at
	// the top level of the entry script.
	game := `<!doctype html><html><head><title>g</title></head><body>` +
		`<script>var best=+(localStorage.getItem('hi')||0);start(best);</script></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(game), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "game.js"), []byte("localStorage.getItem('x')"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .svg also takes the sandbox branch, but it is XML — splicing an HTML
	// <script> into it would corrupt the document.
	if err := os.WriteFile(filepath.Join(dir, "logo.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o644); err != nil {
		t.Fatal(err)
	}

	w, _ := ts.Do(t, "GET", "/play/"+gameID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("serve html: %d", w.Code)
	}
	body := w.Body.String()

	shimAt := strings.Index(body, "window.localStorage")
	if shimAt == -1 {
		t.Fatalf("shim missing from game HTML: %q", body)
	}
	// Ordering is the whole point: landing after the game's script would leave
	// the crash exactly where it was.
	if gameAt := strings.Index(body, "localStorage.getItem('hi')"); gameAt < shimAt {
		t.Errorf("shim at %d must precede the game's own script at %d", shimAt, gameAt)
	}
	// Standards mode: nothing may precede the doctype, or percentage heights
	// break for every full-viewport canvas game.
	if !strings.HasPrefix(body, "<!doctype html>") {
		t.Errorf("doctype must stay first (quirks mode otherwise), got: %.60q", body)
	}
	if !strings.Contains(body, "<script>var best=") {
		t.Error("original game markup must survive injection intact")
	}
	// A wrong Content-Length hangs or truncates the response.
	if cl := w.Header().Get("Content-Length"); cl != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %s != body length %d", cl, len(body))
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	// The sandbox that makes the shim necessary must still be in force.
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox allow-scripts") {
		t.Errorf("sandbox CSP must be unchanged, got: %q", csp)
	}

	// Non-HTML must be served byte-for-byte.
	for _, tc := range []struct{ path, want string }{
		{"/play/" + gameID + "/game.js", "localStorage.getItem('x')"},
		{"/play/" + gameID + "/logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"/>`},
	} {
		w, _ := ts.Do(t, "GET", tc.path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
		if got := w.Body.String(); got != tc.want {
			t.Errorf("%s must not be rewritten, got: %q", tc.path, got)
		}
	}
}

// Injection changes the body length, so a client's byte offsets would no
// longer line up; Range requests must fall through to http.ServeFile.
func TestServe_GameHTMLRangeRequestIsNotInjected(t *testing.T) {
	ts := testutil.NewTestServer(t)

	prevGames := storage.GamesDir
	storage.GamesDir = t.TempDir()
	t.Cleanup(func() { storage.GamesDir = prevGames })
	ts.Engine.GET("/play/:id", handlers.ServeGameFiles("", ""))

	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "Range Game")
	dir := filepath.Join(storage.GamesDir, gameID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><head></head><body>0123456789</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, _ := ts.Do(t, "GET", "/play/"+gameID, nil, testutil.WithHeader("Range", "bytes=0-9"))
	if w.Code != http.StatusPartialContent {
		t.Fatalf("range request should be served by http.ServeFile as 206, got %d", w.Code)
	}
	if body := w.Body.String(); body != "<!doctype " {
		t.Errorf("range body must be unmodified file bytes, got %q", body)
	}
}
