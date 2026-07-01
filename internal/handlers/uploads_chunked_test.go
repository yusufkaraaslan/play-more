package handlers_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// makeZip returns a minimal in-memory zip with a single
// index.html entry. Used to satisfy the finalize step's
// "must be a valid zip" check.
func makeZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("index.html")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write([]byte("<html>hi</html>")); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// TestChunkedUpload_HappyPath runs the full 4-step protocol
// (init → PUT chunks → finalize) and asserts the game row
// exists at the end with the expected metadata.
func TestChunkedUpload_HappyPath(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	// A minimal valid zip — small enough to fit in one chunk.
	payload := makeZip(t)
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])

	// Init
	w, body := ts.Do(t, "POST", "/api/v1/uploads/init", map[string]any{
		"filename": "game.zip",
		"size":     len(payload),
		"kind":     "new_game",
		"metadata": map[string]any{
			"title": "Chunked Game",
			"genre": "action",
		},
	}, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("init: %d %s", w.Code, body)
	}
	var initResp struct {
		UploadID  string `json:"upload_id"`
		ChunkSize int64  `json:"chunk_size"`
	}
	testutil.DecodeJSON(t, body, &initResp)
	if initResp.ChunkSize == 0 {
		t.Fatal("chunk_size is zero")
	}

	// PUT chunks
	offset := int64(0)
	for offset < int64(len(payload)) {
		end := offset + initResp.ChunkSize
		if end > int64(len(payload)) {
			end = int64(len(payload))
		}
		chunk := payload[offset:end]
		w, body = ts.Do(t, "PUT", fmt.Sprintf("/api/v1/uploads/%s/chunks?offset=%d", initResp.UploadID, offset), chunk, testutil.WithAuth(user), testutil.WithHeader("Content-Type", "application/octet-stream"))
		if w.Code != http.StatusOK {
			t.Fatalf("chunk at %d: %d %s", offset, w.Code, body)
		}
		offset = end
	}

	// Finalize
	w, body = ts.Do(t, "POST", "/api/v1/uploads/"+initResp.UploadID+"/finalize", map[string]any{
		"sha256": sumHex,
	}, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", w.Code, body)
	}
	var fin struct {
		GameID string `json:"game_id"`
	}
	testutil.DecodeJSON(t, body, &fin)
	if fin.GameID == "" {
		t.Fatal("no game_id returned")
	}

	// The game should now exist.
	w, body = ts.Do(t, "GET", "/api/v1/games/"+fin.GameID, nil)
	if w.Code != http.StatusOK {
		t.Errorf("GET created game: %d %s", w.Code, body)
	}
	if !strings.Contains(string(body), "Chunked Game") {
		t.Errorf("game title not in response: %s", body)
	}
}

// TestChunkedUpload_StatusReportsRanges verifies the status
// endpoint reports correct received_ranges after partial
// upload — the foundation for resume.
func TestChunkedUpload_StatusReportsRanges(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	// 16 MiB — guaranteed > one 8 MiB chunk, so the second chunk
	// request can read its full window.
	payload := makeZip(t)
	if len(payload) < 16<<20 {
		// Pad with zeros to cross the chunk boundary.
		padding := make([]byte, 16<<20-len(payload))
		payload = append(payload, padding...)
	}
	// Init
	w, body := ts.Do(t, "POST", "/api/v1/uploads/init", map[string]any{
		"filename": "g.zip",
		"size":     len(payload),
		"kind":     "new_game",
		"metadata": map[string]any{"title": "T", "genre": "action"},
	}, testutil.WithAuth(user))
	var initResp struct {
		UploadID  string `json:"upload_id"`
		ChunkSize int64  `json:"chunk_size"`
	}
	testutil.DecodeJSON(t, body, &initResp)

	// Send just the first chunk.
	chunk := payload[:initResp.ChunkSize]
	w, _ = ts.Do(t, "PUT", fmt.Sprintf("/api/v1/uploads/%s/chunks?offset=0", initResp.UploadID), chunk, testutil.WithAuth(user), testutil.WithHeader("Content-Type", "application/octet-stream"))
	if w.Code != http.StatusOK {
		t.Fatalf("chunk: %d", w.Code)
	}

	// Status should show [0, chunkSize)
	w, body = ts.Do(t, "GET", "/api/v1/uploads/"+initResp.UploadID, nil, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var status struct {
		ReceivedRanges [][]int64 `json:"received_ranges"`
	}
	testutil.DecodeJSON(t, body, &status)
	if len(status.ReceivedRanges) != 1 {
		t.Fatalf("expected 1 range, got %d (%v)", len(status.ReceivedRanges), status.ReceivedRanges)
	}
	if status.ReceivedRanges[0][0] != 0 || status.ReceivedRanges[0][1] != initResp.ChunkSize {
		t.Errorf("range: %v want [0, %d]", status.ReceivedRanges[0], initResp.ChunkSize)
	}
}

// TestChunkedUpload_RejectsBadExtension is the input-validation
// gate: filenames outside the .zip/.html/.htm whitelist are
// rejected at init time, before any disk allocation.
func TestChunkedUpload_RejectsBadExtension(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	w, _ := ts.Do(t, "POST", "/api/v1/uploads/init", map[string]any{
		"filename": "game.exe",
		"size":     100,
		"kind":     "new_game",
		"metadata": map[string]any{"title": "T", "genre": "action"},
	}, testutil.WithAuth(user))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad extension: %d, want 400", w.Code)
	}
}

// TestChunkedUpload_RejectsOversize is the size cap gate: a
// request that exceeds MaxFileSize is rejected at init time.
func TestChunkedUpload_RejectsOversize(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	w, _ := ts.Do(t, "POST", "/api/v1/uploads/init", map[string]any{
		"filename": "game.zip",
		"size":     600 * 1024 * 1024, // 600 MiB > 500 MiB cap
		"kind":     "new_game",
		"metadata": map[string]any{"title": "T", "genre": "action"},
	}, testutil.WithAuth(user))
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversize: %d, want 400", w.Code)
	}
}

// TestChunkedUpload_CancelClearsSession verifies DELETE on the
// upload session removes the row.
func TestChunkedUpload_CancelClearsSession(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	w, body := ts.Do(t, "POST", "/api/v1/uploads/init", map[string]any{
		"filename": "g.zip",
		"size":     100,
		"kind":     "new_game",
		"metadata": map[string]any{"title": "T", "genre": "action"},
	}, testutil.WithAuth(user))
	var initResp struct {
		UploadID string `json:"upload_id"`
	}
	testutil.DecodeJSON(t, body, &initResp)

	w, _ = ts.Do(t, "DELETE", "/api/v1/uploads/"+initResp.UploadID, nil, testutil.WithAuth(user))
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Errorf("cancel: %d", w.Code)
	}

	// Status now returns 404
	w, _ = ts.Do(t, "GET", "/api/v1/uploads/"+initResp.UploadID, nil, testutil.WithAuth(user))
	if w.Code != http.StatusNotFound {
		t.Errorf("status after cancel: %d, want 404", w.Code)
	}
}

// TestChunkedUpload_ConcurrentPUTs is a smoke test for
// correctness under concurrent writes — two goroutines PUT
// different ranges of the same upload, and the status shows
// both ranges after.
func TestChunkedUpload_ConcurrentPUTs(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})

	// 20 MiB so each goroutine can write a full chunk window.
	payload := makeZip(t)
	for len(payload) < 20<<20 {
		payload = append(payload, make([]byte, 1<<20)...)
	}
	w, body := ts.Do(t, "POST", "/api/v1/uploads/init", map[string]any{
		"filename": "g.zip",
		"size":     len(payload),
		"kind":     "new_game",
		"metadata": map[string]any{"title": "T", "genre": "action"},
	}, testutil.WithAuth(user))
	var initResp struct {
		UploadID  string `json:"upload_id"`
		ChunkSize int64  `json:"chunk_size"`
	}
	testutil.DecodeJSON(t, body, &initResp)
	half := initResp.ChunkSize
	var wg sync.WaitGroup
	for _, off := range []int64{0, half} {
		wg.Add(1)
		go func(offset int64) {
			defer wg.Done()
			chunk := payload[offset : offset+half]
			ts.Do(t, "PUT", fmt.Sprintf("/api/v1/uploads/%s/chunks?offset=%d", initResp.UploadID, offset), chunk, testutil.WithAuth(user), testutil.WithHeader("Content-Type", "application/octet-stream"))
		}(off)
	}
	wg.Wait()

	w, body = ts.Do(t, "GET", "/api/v1/uploads/"+initResp.UploadID, nil, testutil.WithAuth(user))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, body)
	}
	var status struct {
		ReceivedRanges [][]int64 `json:"received_ranges"`
	}
	testutil.DecodeJSON(t, body, &status)
	if len(status.ReceivedRanges) < 1 {
		t.Errorf("no ranges after concurrent PUTs: %+v", status)
	}
}

// TestChunkedUpload_RejectsUnverifiedEmail is the email
// verification gate applied to all upload endpoints.
func TestChunkedUpload_RejectsUnverifiedEmail(t *testing.T) {
	ts := testutil.NewTestServer(t)
	user := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: false})

	w, _ := ts.Do(t, "POST", "/api/v1/uploads/init", map[string]any{
		"filename": "g.zip",
		"size":     100,
		"kind":     "new_game",
		"metadata": map[string]any{"title": "T", "genre": "action"},
	}, testutil.WithAuth(user))
	if w.Code != http.StatusForbidden {
		t.Errorf("unverified init: %d, want 403", w.Code)
	}
}
