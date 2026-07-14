package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// newSavesTest wires a test server with one verified user owning one
// published game. Rate limits are reset so the per-path save quotas
// (60/min PUT) don't accumulate across tests in the same run.
func newSavesTest(t *testing.T) (*testutil.TestServer, *models.User, string) {
	t.Helper()
	testutil.ResetRateLimits()
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, user.ID, "Save Game "+uuid.NewString()[:8])
	return ts, user, gameID
}

// withGameSessionToken mints a pm_gs_ token for (user, game) and sets
// it as the Bearer credential — the path a sandboxed game iframe uses.
func withGameSessionToken(t *testing.T, user *models.User, gameID string) testutil.ReqOption {
	t.Helper()
	_, rawToken, err := models.MintGameSessionToken(user.ID, gameID, "")
	if err != nil {
		t.Fatalf("mint game session token: %v", err)
	}
	return testutil.WithHeader("Authorization", "Bearer "+rawToken)
}

func TestGameSaves_PutGetRoundTripCookieAuth(t *testing.T) {
	ts, user, gameID := newSavesTest(t)
	value := `{"blocks":[1,2,3],"name":"Crusher"}`

	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/vehicle.main", value, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, body)
	}
	var putResp struct {
		Key       string `json:"key"`
		Size      int    `json:"size"`
		UpdatedAt string `json:"updated_at"`
	}
	testutil.DecodeJSON(t, body, &putResp)
	if putResp.Key != "vehicle.main" || putResp.Size != len(value) || putResp.UpdatedAt == "" {
		t.Fatalf("bad put response: %+v", putResp)
	}

	w, body = ts.Do(t, "GET", "/api/v1/games/"+gameID+"/saves/vehicle.main", nil, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, body)
	}
	var getResp struct {
		Key       string          `json:"key"`
		Value     json.RawMessage `json:"value"`
		UpdatedAt string          `json:"updated_at"`
	}
	testutil.DecodeJSON(t, body, &getResp)
	if getResp.Key != "vehicle.main" || string(getResp.Value) != value || getResp.UpdatedAt == "" {
		t.Fatalf("round-trip mismatch: %+v", getResp)
	}

	// Upsert: overwrite the same key, read the new value back.
	w, body = ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/vehicle.main", `{"blocks":[]}`, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("overwrite: %d %s", w.Code, body)
	}
	_, body = ts.Do(t, "GET", "/api/v1/games/"+gameID+"/saves/vehicle.main", nil, testutil.WithAuth(user))
	testutil.DecodeJSON(t, body, &getResp)
	if string(getResp.Value) != `{"blocks":[]}` {
		t.Fatalf("overwrite not visible: %s", getResp.Value)
	}
}

func TestGameSaves_GameSessionTokenRightGame(t *testing.T) {
	ts, user, gameID := newSavesTest(t)
	auth := withGameSessionToken(t, user, gameID)

	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/progress", `{"level":4}`, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("put with pm_gs_: %d %s", w.Code, body)
	}
	w, body = ts.Do(t, "GET", "/api/v1/games/"+gameID+"/saves/progress", nil, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("get with pm_gs_: %d %s", w.Code, body)
	}
	if !strings.Contains(string(body), `"level":4`) {
		t.Fatalf("value lost: %s", body)
	}
}

func TestGameSaves_GameSessionTokenWrongGame403(t *testing.T) {
	ts, user, gameID := newSavesTest(t)
	otherGame := testutil.SeedGame(t, nil, user.ID, "Other Game "+uuid.NewString()[:8])
	auth := withGameSessionToken(t, user, otherGame)

	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/v1/games/" + gameID + "/saves/progress", `{"level":4}`},
		{"GET", "/api/v1/games/" + gameID + "/saves/progress", nil},
		{"GET", "/api/v1/games/" + gameID + "/saves", nil},
		{"DELETE", "/api/v1/games/" + gameID + "/saves/progress", nil},
	} {
		w, body := ts.Do(t, req.method, req.path, req.body, auth)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s with foreign-game token: %d %s, want 403", req.method, req.path, w.Code, body)
		}
	}
}

func TestGameSaves_Unauthenticated401(t *testing.T) {
	ts, _, gameID := newSavesTest(t)
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/v1/games/" + gameID + "/saves/progress", `{"level":4}`},
		{"GET", "/api/v1/games/" + gameID + "/saves/progress", nil},
		{"GET", "/api/v1/games/" + gameID + "/saves", nil},
		{"DELETE", "/api/v1/games/" + gameID + "/saves/progress", nil},
	} {
		w, _ := ts.Do(t, req.method, req.path, req.body)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated: %d, want 401", req.method, req.path, w.Code)
		}
	}
}

func TestGameSaves_KeyLimit409(t *testing.T) {
	ts, user, gameID := newSavesTest(t)

	// Fill all 32 slots directly through the model (faster than 32 PUTs).
	for i := 0; i < models.MaxGameSaveKeysPerUserGame; i++ {
		if _, err := models.UpsertGameSave(user.ID, gameID, fmt.Sprintf("slot-%02d", i), `{"i":1}`); err != nil {
			t.Fatalf("seed save %d: %v", i, err)
		}
	}

	// Overwriting an EXISTING key at the cap must still succeed.
	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/slot-00", `{"i":2}`, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("overwrite at cap: %d %s", w.Code, body)
	}

	// A NEW key beyond the cap is rejected with 409.
	w, body = ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/slot-32", `{"i":1}`, testutil.WithAuth(user))
	if w.Code != http.StatusConflict {
		t.Fatalf("33rd key: %d %s, want 409", w.Code, body)
	}
	if !strings.Contains(string(body), "key limit") {
		t.Errorf("409 error message unclear: %s", body)
	}

	// The cap is per (user, game) — a different user still has room.
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	w, body = ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/slot-32", `{"i":1}`, testutil.WithAuth(other))
	if w.Code != http.StatusOK {
		t.Fatalf("other user's first key: %d %s", w.Code, body)
	}
}

func TestGameSaves_OversizeValue413(t *testing.T) {
	ts, user, gameID := newSavesTest(t)

	// A JSON string just over the 64 KiB cap (quotes push it past).
	oversize := `"` + strings.Repeat("a", models.MaxGameSaveValueBytes) + `"`
	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/big", oversize, testutil.WithAuth(user))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize value: %d %s, want 413", w.Code, body)
	}

	// Exactly at the cap is fine.
	atCap := `"` + strings.Repeat("a", models.MaxGameSaveValueBytes-2) + `"`
	w, body = ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/big", atCap, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("at-cap value: %d %s", w.Code, body)
	}
}

func TestGameSaves_InvalidKey400(t *testing.T) {
	ts, user, gameID := newSavesTest(t)

	for _, key := range []string{"bad!key", "sl/ash", strings.Repeat("k", 65)} {
		w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/"+key, `{"x":1}`, testutil.WithAuth(user))
		// A key containing "/" doesn't even match the route — Gin 404s
		// before the handler runs. Everything else must 400.
		want := http.StatusBadRequest
		if strings.Contains(key, "/") {
			want = http.StatusNotFound
		}
		if w.Code != want {
			t.Errorf("key %q: %d %s, want %d", key, w.Code, body, want)
		}
	}
}

func TestGameSaves_InvalidJSON400(t *testing.T) {
	ts, user, gameID := newSavesTest(t)

	for name, payload := range map[string]string{
		"malformed": `{"unterminated`,
		"empty":     "",
	} {
		w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/state", payload, testutil.WithAuth(user))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s body: %d %s, want 400", name, w.Code, body)
		}
	}
}

func TestGameSaves_DeleteThenGet404(t *testing.T) {
	ts, user, gameID := newSavesTest(t)

	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/tmp", `{"x":1}`, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, body)
	}
	w, _ = ts.Do(t, "DELETE", "/api/v1/games/"+gameID+"/saves/tmp", nil, testutil.WithAuth(user))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d, want 204", w.Code)
	}
	w, _ = ts.Do(t, "GET", "/api/v1/games/"+gameID+"/saves/tmp", nil, testutil.WithAuth(user))
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d, want 404", w.Code)
	}
	// Idempotent: deleting again is still a 204.
	w, _ = ts.Do(t, "DELETE", "/api/v1/games/"+gameID+"/saves/tmp", nil, testutil.WithAuth(user))
	if w.Code != http.StatusNoContent {
		t.Fatalf("re-delete: %d, want 204", w.Code)
	}
}

func TestGameSaves_ListShowsKeysWithoutValues(t *testing.T) {
	ts, user, gameID := newSavesTest(t)

	valueA := `{"secret":"do-not-leak-a"}`
	valueB := `{"secret":"do-not-leak-b"}`
	ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/alpha", valueA, testutil.WithAuth(user))
	ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/beta", valueB, testutil.WithAuth(user))

	w, body := ts.Do(t, "GET", "/api/v1/games/"+gameID+"/saves", nil, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, body)
	}
	var listResp struct {
		Saves []struct {
			Key       string `json:"key"`
			Size      int    `json:"size"`
			UpdatedAt string `json:"updated_at"`
		} `json:"saves"`
	}
	testutil.DecodeJSON(t, body, &listResp)
	if len(listResp.Saves) != 2 {
		t.Fatalf("list has %d entries, want 2: %s", len(listResp.Saves), body)
	}
	if listResp.Saves[0].Key != "alpha" || listResp.Saves[0].Size != len(valueA) || listResp.Saves[0].UpdatedAt == "" {
		t.Errorf("bad list entry: %+v", listResp.Saves[0])
	}
	if listResp.Saves[1].Key != "beta" || listResp.Saves[1].Size != len(valueB) {
		t.Errorf("bad list entry: %+v", listResp.Saves[1])
	}
	if strings.Contains(string(body), "do-not-leak") {
		t.Errorf("list leaked save values: %s", body)
	}
}

// Sizes must be BYTES everywhere. SQLite LENGTH() on TEXT counts code
// points, which silently diverges from Go's len() the moment a value
// holds non-ASCII (player names, emoji design labels) — so pin it.
func TestGameSaves_ListSizeCountsBytesNotRunes(t *testing.T) {
	ts, user, gameID := newSavesTest(t)

	value := `{"label":"méch 🤖 araç"}`
	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/utf8", value, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, body)
	}
	var putResp struct {
		Size int `json:"size"`
	}
	testutil.DecodeJSON(t, body, &putResp)
	if putResp.Size != len(value) {
		t.Fatalf("put size = %d, want byte length %d", putResp.Size, len(value))
	}

	w, body = ts.Do(t, "GET", "/api/v1/games/"+gameID+"/saves", nil, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, body)
	}
	var listResp struct {
		Saves []struct {
			Key  string `json:"key"`
			Size int    `json:"size"`
		} `json:"saves"`
	}
	testutil.DecodeJSON(t, body, &listResp)
	if len(listResp.Saves) != 1 {
		t.Fatalf("list has %d entries, want 1: %s", len(listResp.Saves), body)
	}
	if listResp.Saves[0].Size != len(value) {
		t.Errorf("list size = %d, want byte length %d (LENGTH must cast to BLOB)",
			listResp.Saves[0].Size, len(value))
	}
}

func TestGameSaves_ScopedPerUser(t *testing.T) {
	ts, user, gameID := newSavesTest(t)
	other := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/mine", `{"x":1}`, testutil.WithAuth(user))

	// Another player sees neither the key nor the listing entry.
	w, _ := ts.Do(t, "GET", "/api/v1/games/"+gameID+"/saves/mine", nil, testutil.WithAuth(other))
	if w.Code != http.StatusNotFound {
		t.Errorf("other user's get: %d, want 404", w.Code)
	}
	w, body := ts.Do(t, "GET", "/api/v1/games/"+gameID+"/saves", nil, testutil.WithAuth(other))
	if w.Code != http.StatusOK || strings.Contains(string(body), "mine") {
		t.Errorf("other user's list: %d %s, want empty 200", w.Code, body)
	}
}

func TestGameSaves_UnpublishedGameHiddenFromNonOwner(t *testing.T) {
	ts, owner, gameID := newSavesTest(t)
	rando := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	if _, err := storage.DB.Exec(`UPDATE games SET published = 0 WHERE id = ?`, gameID); err != nil {
		t.Fatal(err)
	}

	// Non-owners get a 404 — same visibility rule as sdk-token minting.
	w, _ := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/x", `{"x":1}`, testutil.WithAuth(rando))
	if w.Code != http.StatusNotFound {
		t.Errorf("rando put on unpublished game: %d, want 404", w.Code)
	}

	// The developer can still use saves on their unpublished game.
	w, body := ts.Do(t, "PUT", "/api/v1/games/"+gameID+"/saves/x", `{"x":1}`, testutil.WithAuth(owner))
	if w.Code != http.StatusOK {
		t.Errorf("owner put on unpublished game: %d %s", w.Code, body)
	}

	// A nonexistent game 404s too.
	w, _ = ts.Do(t, "GET", "/api/v1/games/no-such-game/saves", nil, testutil.WithAuth(owner))
	if w.Code != http.StatusNotFound {
		t.Errorf("nonexistent game: %d, want 404", w.Code)
	}
}
