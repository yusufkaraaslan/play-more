package middleware

import "testing"

// TestCanonicalRateLimitPath is the regression test for the
// double-quota bug: the /api and /api/v1 mounts must collapse to
// the SAME per-endpoint rate-limit key, or an attacker doubles
// every throttle by alternating prefixes.
func TestCanonicalRateLimitPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Both prefixes of the same endpoint canonicalize equally.
		{"/api/v1/auth/login", "/auth/login"},
		{"/api/auth/login", "/auth/login"},
		{"/api/v1/games/:id/builds", "/games/:id/builds"},
		{"/api/games/:id/builds", "/games/:id/builds"},
		// Prefix roots.
		{"/api/v1", ""},
		{"/api", ""},
		// Non-API paths are untouched.
		{"/deploy.sh", "/deploy.sh"},
		{"/play/:id", "/play/:id"},
		{"", ""},
		// A path that merely starts with "/api" but isn't the mount
		// (no following slash) must not be mangled.
		{"/apiary", "/apiary"},
	}
	for _, c := range cases {
		if got := canonicalRateLimitPath(c.in); got != c.want {
			t.Errorf("canonicalRateLimitPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// The two prefixes must produce identical keys — the actual
	// invariant the fix guarantees.
	if canonicalRateLimitPath("/api/v1/auth/register") != canonicalRateLimitPath("/api/auth/register") {
		t.Fatal("/api and /api/v1 must share a rate-limit bucket")
	}
}
