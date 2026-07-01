package server

import (
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMountAPIRoutes_BothPrefixesAreEquivalent verifies that the
// /api/v1/ and /api/ groups receive the same route table — the same
// methods, the same path templates. This is the invariant that backs
// the "permanent alias" guarantee in routes.go: removing a route from
// one prefix without the other will fail this test in CI.
//
// We partition r.Routes() by longest-matching prefix, then compare
// the (method, suffix) sets between /api/v1 and /api. The "longest
// prefix wins" rule is what makes /api/v1/* not appear in the /api/*
// collection: /api/v1 is a longer matching prefix for those paths.
func TestMountAPIRoutes_BothPrefixesAreEquivalent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := apiConfig{
		uploadCap:   1 << 20,
		imageCap:    1 << 20,
		chunkPutCap: 1 << 20,
	}

	r := gin.New()
	mountAPIRoutes(r.Group("/api/v1"), cfg)
	mountAPIRoutes(r.Group("/api"), cfg)

	// The two prefixes under test. Any route that doesn't fall under
	// one of these is ignored (e.g. /health, /play/:id, /docs which
	// are mounted directly on `r`, not via mountAPIRoutes).
	prefixes := []string{"/api/v1", "/api"}

	buckets := make(map[string]map[string]bool, len(prefixes))
	for _, p := range prefixes {
		buckets[p] = map[string]bool{}
	}
	for _, ri := range r.Routes() {
		if ri.Path == "" {
			continue
		}
		owner := longestPrefix(ri.Path, prefixes)
		if owner == "" {
			continue
		}
		suffix := strings.TrimPrefix(ri.Path, owner)
		if suffix == "" {
			suffix = "/"
		}
		buckets[owner][ri.Method+" "+suffix] = true
	}

	v1Keys := keysSorted(buckets["/api/v1"])
	legacyKeys := keysSorted(buckets["/api"])

	if !reflect.DeepEqual(v1Keys, legacyKeys) {
		t.Errorf("route tables differ between /api/v1 and /api\nv1-only:\n  %s\nlegacy-only:\n  %s",
			joinKeys(diffKeys(v1Keys, legacyKeys)),
			joinKeys(diffKeys(legacyKeys, v1Keys)))
	}

	// Sanity: there should be at least the ~30+ endpoints we know about.
	// If this drops, something has been silently removed.
	if len(v1Keys) < 50 {
		t.Errorf("expected >= 50 routes under /api/v1, got %d", len(v1Keys))
	}
}

// TestMountAPIRoutes_UnknownPrefixFallsThrough documents the behavior
// for a request to a version we don't support (e.g. /api/v2): it
// reaches the NoRoute / SPA fallback. We don't register NoRoute in
// this minimal test, so it should return 404. The test exists to
// pin the behavior; if we ever add an explicit v2 prefix, this test
// will start failing and force the decision.
func TestMountAPIRoutes_UnknownPrefixFallsThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := apiConfig{uploadCap: 1 << 20, imageCap: 1 << 20, chunkPutCap: 1 << 20}
	r := gin.New()
	mountAPIRoutes(r.Group("/api/v1"), cfg)
	mountAPIRoutes(r.Group("/api"), cfg)

	// /api/v2/auth/me is not registered — should 404, not match v1.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v2/auth/me", nil)
	r.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404 for unknown API version, got %d", w.Code)
	}
}

// longestPrefix returns the candidate prefix that is the longest
// strict prefix of path (where "strict" means path == prefix or
// path begins with prefix + "/"). Returns "" if no candidate matches.
func longestPrefix(path string, candidates []string) string {
	best := ""
	for _, c := range candidates {
		if path != c && !strings.HasPrefix(path, c+"/") {
			continue
		}
		if len(c) > len(best) {
			best = c
		}
	}
	return best
}

func keysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func diffKeys(a, b []string) []string {
	bs := make(map[string]bool, len(b))
	for _, k := range b {
		bs[k] = true
	}
	var out []string
	for _, k := range a {
		if !bs[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func joinKeys(ks []string) string {
	if len(ks) == 0 {
		return "(none)"
	}
	return strings.Join(ks, "\n  ")
}
