package handlers_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// The SPA re-mints a pm_gs_ token every ~4 minutes for the whole of a play
// session (~16 mints/hour per game), so the mint route's per-IP limit can no
// longer be the primary meter: players who share an egress IP — a household,
// a dorm, CGNAT — would throttle each other out of multiplayer entirely. The
// per-IP limit is loose (600/hour) and the real guard is per-account.
//
// Four accounts each minting a session's worth of tokens from one IP is 64
// requests, which is over the 60/hour the route used to allow.
func TestMintSDKToken_SharedIPDoesNotThrottleOtherAccounts(t *testing.T) {
	testutil.ResetRateLimits()
	ts := testutil.NewTestServer(t)

	const sharedIP = "203.0.113.77"
	const mintsPerUser = 16

	for u := 0; u < 4; u++ {
		user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
		gameID := testutil.SeedGame(t, nil, user.ID, "Shared IP "+uuid.NewString()[:8])

		for i := 0; i < mintsPerUser; i++ {
			w, body := ts.Do(t, "POST", "/api/v1/games/"+gameID+"/sdk-token", "{}",
				testutil.WithAuth(user), testutil.WithIP(sharedIP))
			if w.Code != http.StatusCreated {
				t.Fatalf("user %d mint %d from shared IP: got %d, want 201 (body: %s)", u, i, w.Code, body)
			}
		}
	}
}

// The per-account guard is keyed on the user, not the request IP, so rotating
// source addresses must not buy more mints. Burn the account's hourly quota
// directly (minting 180 times over HTTP would trip
// MaxActiveGameSessionTokensPerUser first), then confirm the handler rejects
// the next request even from a fresh IP.
func TestMintSDKToken_PerAccountLimitIsNotPerIP(t *testing.T) {
	testutil.ResetRateLimits()
	ts := testutil.NewTestServer(t)

	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, user.ID, "Account Limit "+uuid.NewString()[:8])

	// One mint from a fresh IP succeeds before the quota is spent.
	w, body := ts.Do(t, "POST", "/api/v1/games/"+gameID+"/sdk-token", "{}",
		testutil.WithAuth(user), testutil.WithIP("198.51.100.1"))
	if w.Code != http.StatusCreated {
		t.Fatalf("baseline mint: got %d, want 201 (body: %s)", w.Code, body)
	}

	// Spend the rest of the account's hourly allowance.
	for i := 0; i < 200; i++ {
		middleware.AllowByKey("sdktoken:"+user.ID, 180, 3600)
	}

	// A brand-new IP must not reset it — the meter is the account.
	for i, ip := range []string{"198.51.100.2", "198.51.100.3", "2001:db8::1"} {
		w, body := ts.Do(t, "POST", "/api/v1/games/"+gameID+"/sdk-token", "{}",
			testutil.WithAuth(user), testutil.WithIP(ip))
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("mint %d from new IP %s after account quota exhausted: got %d, want 429 (body: %s)",
				i, ip, w.Code, body)
		}
	}
}

// A 429 from the per-account guard must not leak whether an unpublished game
// exists: the quota check runs before the game lookup, so both a real and a
// bogus game id return the same 429.
func TestMintSDKToken_QuotaCheckPrecedesGameLookup(t *testing.T) {
	testutil.ResetRateLimits()
	ts := testutil.NewTestServer(t)

	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	for i := 0; i < 200; i++ {
		middleware.AllowByKey("sdktoken:"+user.ID, 180, 3600)
	}

	for _, gameID := range []string{uuid.NewString(), "definitely-not-a-game"} {
		w, body := ts.Do(t, "POST", "/api/v1/games/"+gameID+"/sdk-token", "{}", testutil.WithAuth(user))
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("mint for %q with quota exhausted: got %d, want 429 (body: %s)", gameID, w.Code, body)
		}
	}
}
