# Chunked Upload Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `/api/uploads/{init,chunks,status,finalize,cancel}` so games larger than Cloudflare's 100 MiB body cap can be uploaded behind any reverse-proxy with body limits ≥ 9 MiB, coexisting with the existing single-shot `POST /api/games` endpoints (64 MiB client-side threshold).

**Architecture:** Upload-first protocol. Client `init`s a session (metadata bundled), `PUT`s chunks at byte offsets into a pre-allocated sparse file via `os.File.WriteAt`, then `finalize`s — the partial file *is* the final file, no concat step. Full resume via `received_ranges` JSON in a new `upload_sessions` table. SHA256 required from CLI, optional from SPA. New GC goroutine sweeps expired sessions + orphan files.

**Tech Stack:** Go 1.x stdlib (`crypto/sha256`, `os.File.WriteAt`, `encoding/json`, `sync.Mutex`); Gin; `modernc.org/sqlite` (pure Go, already used); vanilla JS (no new deps); Bash + curl + dd + sha256sum/shasum.

**Spec:** `docs/superpowers/specs/2026-05-21-chunked-upload-design.md` (commit `07c25b0`).

**Testing note:** playmore has no automated test suite. This plan introduces Go stdlib unit tests **only for the pure range-math functions in `internal/models/upload_session.go`** — the rest is verified by manual curl + browser steps. The range math is fiddly stateful logic where unit tests pay off; handler-layer testing would need `httptest` scaffolding not currently in the repo.

---

## File Structure

**New files (5):**

| File                                       | Responsibility                                                                                          |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------- |
| `internal/models/upload_session.go`        | `UploadSession` struct, DB CRUD, range-math (`AddRange`, `IsComplete`, `MissingRanges`).                |
| `internal/models/upload_session_test.go`   | Go stdlib unit tests for range math.                                                                    |
| `internal/storage/partial.go`              | Partial-file helpers — `CreatePartial`, `WritePartialAt`, `OpenPartial`, `DeletePartial`, `ListPartialIDs`. |
| `internal/handlers/uploads_chunked.go`     | 5 HTTP handlers — `InitUpload`, `PutChunk`, `GetUploadStatus`, `FinalizeUpload`, `CancelUpload`.        |
| `internal/storage/partial_gc.go`           | `StartPartialGC(ctx)` goroutine — expired-session sweep + orphan-file sweep.                            |

**Modified files (8):**

| File                                       | Change                                                                                                  |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------- |
| `internal/storage/db.go`                   | Append `upload_sessions` migration to migrations slice.                                                 |
| `internal/server/server.go`                | Wire 5 new routes with auth + verified-email + rate-limit + per-route body cap.                         |
| `internal/middleware/csrf.go`              | Allow `application/octet-stream` only on `PUT /api/uploads/:upload_id/chunks`.                          |
| `main.go`                                  | Call `storage.StartPartialGC(ctx)` alongside other background goroutines.                               |
| `frontend/index.html`                      | `uploadGameChunked` helper, 64 MiB branching at the 2 call sites, progress bar, resume UI.              |
| `internal/handlers/playmore-deploy.sh`     | `chunked_push` function, threshold branching, sha256 detection, `dd` chunk loop, resume on rerun.       |
| `docs/DEVELOPER.md`                        | Append "Chunked uploads" API section.                                                                   |
| `docs/SETUP.md` + `README.md`              | Soften CF body-cap warnings; mention chunked support.                                                   |

---

### Task 1: DB migration for `upload_sessions`

**Files:**
- Modify: `internal/storage/db.go` (append to migrations slice around line 80)

- [ ] **Step 1: Append migration entries**

Open `internal/storage/db.go`. Find the migrations slice (starts at line ~40, currently ends around line 80 with `CREATE INDEX IF NOT EXISTS idx_game_views_created ON game_views(created_at)`). Append these three entries before the closing `}`:

```go
		`CREATE TABLE IF NOT EXISTS upload_sessions (
			id              TEXT PRIMARY KEY,
			user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			game_id         TEXT REFERENCES games(id) ON DELETE CASCADE,
			kind            TEXT NOT NULL,
			filename        TEXT NOT NULL,
			size            INTEGER NOT NULL,
			received_ranges TEXT NOT NULL DEFAULT '[]',
			metadata_json   TEXT NOT NULL DEFAULT '{}',
			sha256_expected TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'open',
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at      DATETIME NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_sessions_user    ON upload_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires ON upload_sessions(expires_at)`,
```

- [ ] **Step 2: Build to verify compilation**

Run: `go build -o playmore`
Expected: success, binary produced.

- [ ] **Step 3: Run server against a temp data dir + verify table exists**

```bash
mkdir -p /tmp/playmore-test-data
./playmore --port 18080 --data /tmp/playmore-test-data &
PID=$!
sleep 1
kill $PID
sqlite3 /tmp/playmore-test-data/playmore.db ".schema upload_sessions"
```
Expected: schema printout matching the CREATE TABLE above.

Also verify indexes:
```bash
sqlite3 /tmp/playmore-test-data/playmore.db ".indexes upload_sessions"
```
Expected output (whitespace may vary):
```
idx_upload_sessions_expires  idx_upload_sessions_user
```

- [ ] **Step 4: Verify idempotency**

Start the server again against the same dir, kill it, check schema is still intact:
```bash
./playmore --port 18080 --data /tmp/playmore-test-data &
sleep 1; kill %1
sqlite3 /tmp/playmore-test-data/playmore.db ".schema upload_sessions" | head -1
```
Expected: still prints `CREATE TABLE upload_sessions(...)`. No errors in playmore log.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/db.go
git commit -m "feat: chunked upload — add upload_sessions migration"
```

---

### Task 2: `UploadSession` model + range math + unit tests

**Files:**
- Create: `internal/models/upload_session.go`
- Create: `internal/models/upload_session_test.go`

- [ ] **Step 1: Write failing unit tests first**

Create `internal/models/upload_session_test.go`:

```go
package models

import (
	"reflect"
	"testing"
)

func TestAddRange_emptyStart(t *testing.T) {
	got := AddRange(nil, 0, 100)
	want := [][2]int64{{0, 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_appendsContiguous(t *testing.T) {
	got := AddRange([][2]int64{{0, 100}}, 100, 200)
	want := [][2]int64{{0, 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_appendsDisjoint(t *testing.T) {
	got := AddRange([][2]int64{{0, 100}}, 200, 300)
	want := [][2]int64{{0, 100}, {200, 300}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_fillsGap(t *testing.T) {
	got := AddRange([][2]int64{{0, 100}, {200, 300}}, 100, 200)
	want := [][2]int64{{0, 300}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_overlapMerges(t *testing.T) {
	got := AddRange([][2]int64{{0, 100}}, 50, 150)
	want := [][2]int64{{0, 150}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_fullyContained(t *testing.T) {
	got := AddRange([][2]int64{{0, 200}}, 50, 100)
	want := [][2]int64{{0, 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_outOfOrderInsert(t *testing.T) {
	got := AddRange([][2]int64{{200, 300}}, 0, 100)
	want := [][2]int64{{0, 100}, {200, 300}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestIsComplete_singleFullRange(t *testing.T) {
	if !IsComplete([][2]int64{{0, 500}}, 500) {
		t.Fatal("expected complete")
	}
}

func TestIsComplete_gap(t *testing.T) {
	if IsComplete([][2]int64{{0, 100}, {200, 500}}, 500) {
		t.Fatal("expected incomplete (gap)")
	}
}

func TestIsComplete_short(t *testing.T) {
	if IsComplete([][2]int64{{0, 499}}, 500) {
		t.Fatal("expected incomplete (short)")
	}
}

func TestIsComplete_empty(t *testing.T) {
	if IsComplete(nil, 500) {
		t.Fatal("expected incomplete (empty)")
	}
}

func TestMissingRanges_oneGap(t *testing.T) {
	got := MissingRanges([][2]int64{{0, 100}, {200, 500}}, 500)
	want := [][2]int64{{100, 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMissingRanges_trailing(t *testing.T) {
	got := MissingRanges([][2]int64{{0, 100}}, 500)
	want := [][2]int64{{100, 500}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMissingRanges_leading(t *testing.T) {
	got := MissingRanges([][2]int64{{100, 500}}, 500)
	want := [][2]int64{{0, 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMissingRanges_complete(t *testing.T) {
	got := MissingRanges([][2]int64{{0, 500}}, 500)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestMissingRanges_empty(t *testing.T) {
	got := MissingRanges(nil, 500)
	want := [][2]int64{{0, 500}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestReceivedBytes_contiguousFromZero(t *testing.T) {
	if got := ReceivedBytes([][2]int64{{0, 100}, {200, 300}}); got != 100 {
		t.Fatalf("got %d want 100", got)
	}
}

func TestReceivedBytes_emptyOrGapAtStart(t *testing.T) {
	if got := ReceivedBytes(nil); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
	if got := ReceivedBytes([][2]int64{{50, 100}}); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/models/... -run UploadSession -v`
Expected: compilation errors — `AddRange`, `IsComplete`, `MissingRanges`, `ReceivedBytes` are undefined.

- [ ] **Step 3: Implement `internal/models/upload_session.go`**

Create the file:

```go
package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// UploadSession is a row in upload_sessions tracking an in-progress chunked upload.
type UploadSession struct {
	ID             string
	UserID         string
	GameID         sql.NullString
	Kind           string // "new_game" | "reupload"
	Filename       string
	Size           int64
	ReceivedRanges [][2]int64 // sorted, non-overlapping [start, end)
	MetadataJSON   string
	SHA256Expected string
	Status         string // "open" | "finalizing" | "done" | "failed"
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// CreateUploadSession inserts a new row and returns the populated session.
// Caller must populate Kind, Filename, Size, MetadataJSON, optionally GameID.
func CreateUploadSession(s *UploadSession, ttl time.Duration) error {
	s.ID = uuid.New().String()
	s.Status = "open"
	s.CreatedAt = time.Now().UTC()
	s.ExpiresAt = s.CreatedAt.Add(ttl)
	s.ReceivedRanges = nil

	_, err := db().Exec(`INSERT INTO upload_sessions
		(id, user_id, game_id, kind, filename, size, received_ranges, metadata_json,
		 sha256_expected, status, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, '[]', ?, '', 'open', ?, ?)`,
		s.ID, s.UserID, s.GameID, s.Kind, s.Filename, s.Size,
		s.MetadataJSON, s.CreatedAt, s.ExpiresAt)
	return err
}

// GetUploadSession returns the session by id, or sql.ErrNoRows if missing.
func GetUploadSession(id string) (*UploadSession, error) {
	row := db().QueryRow(`SELECT id, user_id, game_id, kind, filename, size,
		received_ranges, metadata_json, sha256_expected, status, created_at, expires_at
		FROM upload_sessions WHERE id = ?`, id)
	var s UploadSession
	var rangesJSON string
	if err := row.Scan(&s.ID, &s.UserID, &s.GameID, &s.Kind, &s.Filename, &s.Size,
		&rangesJSON, &s.MetadataJSON, &s.SHA256Expected, &s.Status, &s.CreatedAt, &s.ExpiresAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(rangesJSON), &s.ReceivedRanges); err != nil {
		return nil, fmt.Errorf("decode received_ranges: %w", err)
	}
	return &s, nil
}

// UpdateReceivedRanges writes the (already-coalesced) ranges back to the row.
// Returns the rows-affected count; 0 means the row was deleted between fetch and update.
func UpdateReceivedRanges(id string, ranges [][2]int64) (int64, error) {
	buf, err := json.Marshal(ranges)
	if err != nil {
		return 0, err
	}
	res, err := db().Exec(`UPDATE upload_sessions SET received_ranges = ? WHERE id = ?`,
		string(buf), id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkFinalizing atomically flips status open→finalizing. Returns true if this
// caller won the race; false means another caller already started finalize.
func MarkFinalizing(id, sha256 string) (bool, error) {
	res, err := db().Exec(`UPDATE upload_sessions
		SET status = 'finalizing', sha256_expected = ?
		WHERE id = ? AND status = 'open'`, sha256, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkStatus sets the status column (used for 'done' / 'failed').
func MarkStatus(id, status string) error {
	_, err := db().Exec(`UPDATE upload_sessions SET status = ? WHERE id = ?`, status, id)
	return err
}

// DeleteUploadSession removes the row by id.
func DeleteUploadSession(id string) error {
	_, err := db().Exec(`DELETE FROM upload_sessions WHERE id = ?`, id)
	return err
}

// ExpiredOpenSessionIDs returns ids of sessions whose expires_at is in the past
// and whose status is still 'open' — these are GC candidates.
func ExpiredOpenSessionIDs(now time.Time) ([]string, error) {
	rows, err := db().Query(`SELECT id FROM upload_sessions
		WHERE expires_at < ? AND status = 'open'`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AllSessionIDs returns the set of session IDs currently in the table —
// used by the orphan-file sweep to detect partial files with no row.
func AllSessionIDs() (map[string]struct{}, error) {
	rows, err := db().Query(`SELECT id FROM upload_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// AddRange inserts [start, end) into sorted-non-overlapping ranges, coalescing.
// Pure function — does not mutate the input slice.
func AddRange(ranges [][2]int64, start, end int64) [][2]int64 {
	if start >= end {
		return ranges
	}
	out := make([][2]int64, 0, len(ranges)+1)
	out = append(out, ranges...)
	out = append(out, [2]int64{start, end})
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	// Coalesce
	merged := out[:0]
	for _, r := range out {
		if n := len(merged); n > 0 && r[0] <= merged[n-1][1] {
			if r[1] > merged[n-1][1] {
				merged[n-1][1] = r[1]
			}
			continue
		}
		merged = append(merged, r)
	}
	// Trim the underlying array to avoid leaking capacity
	result := make([][2]int64, len(merged))
	copy(result, merged)
	return result
}

// IsComplete returns true if ranges == [[0, size)] (one contiguous range covering all bytes).
func IsComplete(ranges [][2]int64, size int64) bool {
	return len(ranges) == 1 && ranges[0][0] == 0 && ranges[0][1] == size
}

// MissingRanges returns the [start, end) ranges within [0, size) not covered by `ranges`.
func MissingRanges(ranges [][2]int64, size int64) [][2]int64 {
	var missing [][2]int64
	cursor := int64(0)
	for _, r := range ranges {
		if r[0] > cursor {
			missing = append(missing, [2]int64{cursor, r[0]})
		}
		if r[1] > cursor {
			cursor = r[1]
		}
	}
	if cursor < size {
		missing = append(missing, [2]int64{cursor, size})
	}
	return missing
}

// ReceivedBytes returns the count of contiguous bytes received starting from offset 0.
// Used for the quick happy-path progress indicator returned from PUT chunk.
func ReceivedBytes(ranges [][2]int64) int64 {
	if len(ranges) == 0 || ranges[0][0] != 0 {
		return 0
	}
	return ranges[0][1]
}

// ErrSessionNotOpen is returned when a chunk write is attempted against a session
// that is not in 'open' status (finalizing/done/failed).
var ErrSessionNotOpen = errors.New("upload session not open")
```

Also need a `db()` helper. Check whether existing models use a package-level `DB` from storage or a private `db()` — look at `internal/models/user.go` or similar:

Run: `grep -n "storage.DB\|db\(\)\|var db\|func db" internal/models/*.go | head -10`

If the convention is `storage.DB.Exec(...)`, replace every `db().Exec(...)` / `db().Query(...)` / `db().QueryRow(...)` above with `storage.DB.Exec(...)` (and add `"github.com/yusufkaraaslan/play-more/internal/storage"` to the import block). Otherwise add the helper:

```go
func db() *sql.DB { return storage.DB }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/models/... -run UploadSession -v`
Expected: all 17 tests PASS.

- [ ] **Step 5: Verify full build still works**

Run: `go build -o playmore`
Expected: success.

- [ ] **Step 6: Commit**

```bash
git add internal/models/upload_session.go internal/models/upload_session_test.go
git commit -m "feat: chunked upload — UploadSession model + range math with unit tests"
```

---

### Task 3: Partial file storage helpers

**Files:**
- Create: `internal/storage/partial.go`

- [ ] **Step 1: Create the file**

Create `internal/storage/partial.go`:

```go
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// partialDir returns the directory where in-progress chunked uploads live.
// Path: {dataDir}/uploads/.partial/. dataDir is the parent of GamesDir.
func partialDir() string {
	return filepath.Join(filepath.Dir(GamesDir), "uploads", ".partial")
}

// partialPath returns the on-disk path for upload id `id`.
func partialPath(id string) string {
	return filepath.Join(partialDir(), id+".bin")
}

// CreatePartial creates a sparse file of length `size` for upload `id`.
// Idempotent — if the file already exists with the right size, it's a no-op;
// if it exists with a different size, returns an error.
func CreatePartial(id string, size int64) error {
	if err := os.MkdirAll(partialDir(), 0o750); err != nil {
		return fmt.Errorf("mkdir partial dir: %w", err)
	}
	p := partialPath(id)
	if fi, err := os.Stat(p); err == nil {
		if fi.Size() != size {
			return fmt.Errorf("partial file already exists at unexpected size: got %d want %d", fi.Size(), size)
		}
		return nil
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create partial: %w", err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		os.Remove(p)
		return fmt.Errorf("truncate partial: %w", err)
	}
	return nil
}

// WritePartialAt writes `len(buf)` bytes at `offset` into the partial file for `id`.
// Returns an error if the write would extend past the file's allocated size.
func WritePartialAt(id string, offset int64, buf []byte) error {
	p := partialPath(id)
	f, err := os.OpenFile(p, os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open partial: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat partial: %w", err)
	}
	if offset+int64(len(buf)) > fi.Size() {
		return fmt.Errorf("write past end: offset=%d len=%d size=%d", offset, len(buf), fi.Size())
	}
	if _, err := f.WriteAt(buf, offset); err != nil {
		return fmt.Errorf("write partial: %w", err)
	}
	return nil
}

// OpenPartial opens the partial file read-only. Caller must close.
// Returned *os.File satisfies io.ReaderAt — fits storage.ExtractZipFromReader directly.
func OpenPartial(id string) (*os.File, error) {
	return os.Open(partialPath(id))
}

// DeletePartial removes the partial file. Returns nil if it doesn't exist.
func DeletePartial(id string) error {
	err := os.Remove(partialPath(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListPartialIDs returns the upload IDs of all partial files on disk.
// Used by the GC orphan sweep.
func ListPartialIDs() ([]string, error) {
	entries, err := os.ReadDir(partialDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".bin") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".bin"))
	}
	return ids, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build -o playmore`
Expected: success.

- [ ] **Step 3: Smoke test partial-file primitives via a throwaway script**

Create `/tmp/partial_smoke.go`:

```go
package main

import (
	"bytes"
	"fmt"
	"log"
	"os"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

func main() {
	if err := storage.InitFileStorage("/tmp/playmore-test-data"); err != nil {
		log.Fatal(err)
	}
	id := "test-upload-001"
	defer storage.DeletePartial(id)

	if err := storage.CreatePartial(id, 100); err != nil {
		log.Fatal(err)
	}
	if err := storage.WritePartialAt(id, 0, []byte("hello world!")); err != nil {
		log.Fatal(err)
	}
	if err := storage.WritePartialAt(id, 50, []byte("middle")); err != nil {
		log.Fatal(err)
	}
	if err := storage.WritePartialAt(id, 200, []byte("over")); err == nil {
		log.Fatal("expected error on out-of-bounds write")
	}

	f, err := storage.OpenPartial(id)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 100)
	if _, err := f.ReadAt(buf, 0); err != nil {
		log.Fatal(err)
	}
	if !bytes.Equal(buf[0:12], []byte("hello world!")) {
		log.Fatalf("head wrong: %q", buf[0:12])
	}
	if !bytes.Equal(buf[50:56], []byte("middle")) {
		log.Fatalf("mid wrong: %q", buf[50:56])
	}
	fmt.Println("OK")
	_ = os.Remove
}
```

Run: `go run /tmp/partial_smoke.go`
Expected output: `OK`

Then clean up: `rm /tmp/partial_smoke.go`

- [ ] **Step 4: Commit**

```bash
git add internal/storage/partial.go
git commit -m "feat: chunked upload — partial-file storage helpers"
```

---

### Task 4: `InitUpload` handler

**Files:**
- Create: `internal/handlers/uploads_chunked.go`

- [ ] **Step 1: Create the handler file with InitUpload + shared helpers**

Create `internal/handlers/uploads_chunked.go`:

```go
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// ChunkSize is the server-recommended chunk size returned by /init.
// Clients should not exceed (ChunkSize + 1 MiB headroom) in a single PUT.
const ChunkSize int64 = 8 << 20 // 8 MiB

// SessionTTL is the lifetime of an upload session from creation.
const SessionTTL = 24 * time.Hour

// sessionLocks gives each upload_id its own mutex so concurrent PUTs for the
// same upload serialize on the read-modify-write of received_ranges.
var sessionLocks sync.Map

func sessionLock(id string) *sync.Mutex {
	v, _ := sessionLocks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// initReq is the JSON body of POST /api/uploads/init.
type initReq struct {
	Filename string          `json:"filename"`
	Size     int64           `json:"size"`
	Kind     string          `json:"kind"`
	GameID   string          `json:"game_id,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// initResp is the JSON response body of /init.
type initResp struct {
	UploadID  string    `json:"upload_id"`
	ChunkSize int64     `json:"chunk_size"`
	ExpiresAt time.Time `json:"expires_at"`
}

// gameMetadata is the schema of the `metadata` field for kind=new_game.
type gameMetadata struct {
	Title       string   `json:"title"`
	Genre       string   `json:"genre"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	IsWebGPU    bool     `json:"is_webgpu,omitempty"`
}

// InitUpload handles POST /api/uploads/init.
func InitUpload(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var req initReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	// Validate filename
	safe := storage.SanitizeFileName(req.Filename)
	if safe == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
		return
	}
	if req.Size <= 0 || req.Size > storage.MaxFileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "size out of range"})
		return
	}

	// Validate kind + companion fields
	s := &models.UploadSession{
		UserID:   user.ID,
		Kind:     req.Kind,
		Filename: safe,
		Size:     req.Size,
	}
	switch req.Kind {
	case "new_game":
		if req.GameID != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "game_id must be absent for kind=new_game"})
			return
		}
		if len(req.Metadata) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "metadata required for kind=new_game"})
			return
		}
		var meta gameMetadata
		if err := json.Unmarshal(req.Metadata, &meta); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid metadata"})
			return
		}
		if meta.Title == "" || meta.Genre == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title and genre required in metadata"})
			return
		}
		s.MetadataJSON = string(req.Metadata)
	case "reupload":
		if req.GameID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "game_id required for kind=reupload"})
			return
		}
		if len(req.Metadata) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "metadata must be absent for kind=reupload"})
			return
		}
		// Existence + ownership check: 404 on either miss
		g, err := models.GetGame(req.GameID)
		if err != nil || g == nil || g.DeveloperID != user.ID {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		s.GameID = sql.NullString{String: req.GameID, Valid: true}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid kind"})
		return
	}

	if err := models.CreateUploadSession(s, SessionTTL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}
	if err := storage.CreatePartial(s.ID, s.Size); err != nil {
		_ = models.DeleteUploadSession(s.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate storage"})
		return
	}

	c.JSON(http.StatusOK, initResp{
		UploadID:  s.ID,
		ChunkSize: ChunkSize,
		ExpiresAt: s.ExpiresAt,
	})
}
```

Note: check the actual `models.GetGame` signature — it might be `models.GetGameByID(id)`. Run `grep -n "func GetGame\|func GetGameByID" internal/models/game.go` and use the existing function. Same for the developer/owner field on the Game struct (could be `DeveloperID`, `UserID`, or `OwnerID`).

- [ ] **Step 2: Verify build**

Run: `go build -o playmore`
Expected: success. If a function/field name differs, fix here.

- [ ] **Step 3: Wire a temporary route to smoke-test (will be replaced in Task 10)**

Add this temporary route registration to verify the handler works. In `internal/server/server.go`, near the existing `api.POST("/games", ...)` line, add:

```go
api.POST("/uploads/init", middleware.AuthRequired(), handlers.RequireVerifiedEmail(),
	middleware.RateLimit(20, 3600), limitBody(1<<20), handlers.InitUpload)
```

(This is temporary; Task 10 will move all 5 routes to a dedicated group.)

- [ ] **Step 4: Manual smoke test**

```bash
go build -o playmore && ./playmore --port 18080 --data /tmp/playmore-test-data &
sleep 1
# Register + login + get session cookie or API key — use existing seed flow:
curl -s -X POST localhost:18080/api/seed
# Acquire an API key for the seeded admin user via the SPA, or use a session
# cookie from a logged-in browser. Save it as $KEY for the curl below.

# Bad: missing metadata for new_game
curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"filename":"game.zip","size":100,"kind":"new_game"}'
# Expected: {"error":"metadata required for kind=new_game"}

# Bad: metadata with no title/genre
curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"filename":"game.zip","size":100,"kind":"new_game","metadata":{}}'
# Expected: {"error":"title and genre required in metadata"}

# Good
curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"filename":"game.zip","size":100,"kind":"new_game","metadata":{"title":"T","genre":"action"}}'
# Expected: {"upload_id":"<uuid>","chunk_size":8388608,"expires_at":"..."}

# Verify DB + partial file
sqlite3 /tmp/playmore-test-data/playmore.db "SELECT id, kind, filename, size, status FROM upload_sessions" 
ls -la /tmp/playmore-test-data/uploads/.partial/
# Expected: one row, one file <uuid>.bin sized at 100 bytes (sparse)
```

Kill the server: `kill %1`

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/uploads_chunked.go internal/server/server.go
git commit -m "feat: chunked upload — InitUpload handler"
```

---

### Task 5: `PutChunk` handler + CSRF allowlist

**Files:**
- Modify: `internal/handlers/uploads_chunked.go` (append PutChunk)
- Modify: `internal/middleware/csrf.go`

- [ ] **Step 1: Inspect csrf.go to see current allowlist**

Run: `cat internal/middleware/csrf.go`

Note the structure — likely a function that returns 400/403 if Content-Type isn't in an allowlist when the request is state-changing and from a browser (Origin/Referer set). API-key auth typically bypasses.

- [ ] **Step 2: Extend CSRF allowlist for the chunk endpoint**

In `internal/middleware/csrf.go`, find the content-type check. Add an explicit exception:

```go
// Existing logic checks ct is one of: application/json, multipart/form-data, ...
// Add: PUT /api/uploads/:upload_id/chunks may also use application/octet-stream.
if c.Request.Method == http.MethodPut &&
	strings.HasPrefix(c.Request.URL.Path, "/api/uploads/") &&
	strings.HasSuffix(c.Request.URL.Path, "/chunks") &&
	ct == "application/octet-stream" {
	// allowed
} else if !allowedContentType(ct) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid content type"})
	return
}
```

Adjust to match the exact existing pattern — the goal is: `application/octet-stream` is permitted only when method=PUT and path matches the chunk route.

- [ ] **Step 3: Append PutChunk to uploads_chunked.go**

Append to `internal/handlers/uploads_chunked.go`:

```go
// putChunkResp is the JSON returned from a successful PUT chunk.
type putChunkResp struct {
	ReceivedBytes int64 `json:"received_bytes"`
}

// PutChunk handles PUT /api/uploads/:upload_id/chunks?offset=N.
func PutChunk(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id := c.Param("upload_id")
	offsetStr := c.Query("offset")
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil || offset < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid offset"})
		return
	}

	// Per-session lock for the read-modify-write of received_ranges
	lock := sessionLock(id)
	lock.Lock()
	defer lock.Unlock()

	s, err := models.GetUploadSession(id)
	if err == sql.ErrNoRows || s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session lookup failed"})
		return
	}
	if s.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if s.Status != "open" {
		c.JSON(http.StatusConflict, gin.H{"error": "session not open"})
		return
	}

	// Read body — Gin's already wrapped in MaxBytesReader at the route layer
	// (cap = ChunkSize + 1 MiB headroom).
	buf, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "chunk too large"})
		return
	}
	n := int64(len(buf))
	if n == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty body"})
		return
	}
	if offset+n > s.Size {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chunk exceeds declared size"})
		return
	}

	if err := storage.WritePartialAt(id, offset, buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write failed"})
		return
	}

	newRanges := models.AddRange(s.ReceivedRanges, offset, offset+n)
	if _, err := models.UpdateReceivedRanges(id, newRanges); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "range update failed"})
		return
	}

	c.JSON(http.StatusOK, putChunkResp{ReceivedBytes: models.ReceivedBytes(newRanges)})
}
```

Add to imports at top of `uploads_chunked.go`:

```go
import (
	"io"
	"strconv"
	// existing imports...
)
```

- [ ] **Step 4: Wire temporary chunk route**

Add to `internal/server/server.go` right below the init route:

```go
api.PUT("/uploads/:upload_id/chunks", middleware.AuthRequired(), handlers.RequireVerifiedEmail(),
	middleware.RateLimit(2000, 3600), limitBody((8<<20)+(1<<20)), handlers.PutChunk)
```

- [ ] **Step 5: Build + manual test**

```bash
go build -o playmore && ./playmore --port 18080 --data /tmp/playmore-test-data &
sleep 1

# Init a 100-byte session and capture upload_id
UID=$(curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"filename":"game.zip","size":100,"kind":"new_game","metadata":{"title":"T","genre":"action"}}' \
  | grep -o '"upload_id":"[^"]*"' | cut -d'"' -f4)
echo "upload_id=$UID"

# Write 50 bytes at offset 0
printf 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' | \
  curl -s -X PUT --data-binary @- \
    -H "Authorization: Bearer $KEY" -H "Content-Type: application/octet-stream" \
    "localhost:18080/api/uploads/$UID/chunks?offset=0"
# Expected: {"received_bytes":50}

# Write 50 bytes at offset 50 (completes)
printf 'BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB' | \
  curl -s -X PUT --data-binary @- \
    -H "Authorization: Bearer $KEY" -H "Content-Type: application/octet-stream" \
    "localhost:18080/api/uploads/$UID/chunks?offset=50"
# Expected: {"received_bytes":100}

# Inspect file
xxd /tmp/playmore-test-data/uploads/.partial/$UID.bin | head
# Expected: 50 'A' then 50 'B'

# Negative — chunk past end
printf 'C' | curl -s -X PUT --data-binary @- \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/octet-stream" \
  "localhost:18080/api/uploads/$UID/chunks?offset=100"
# Expected: {"error":"chunk exceeds declared size"}
```

Kill the server: `kill %1`

- [ ] **Step 6: Commit**

```bash
git add internal/handlers/uploads_chunked.go internal/middleware/csrf.go internal/server/server.go
git commit -m "feat: chunked upload — PutChunk handler + CSRF octet-stream allowlist"
```

---

### Task 6: `GetUploadStatus` + `CancelUpload` handlers

**Files:**
- Modify: `internal/handlers/uploads_chunked.go`

- [ ] **Step 1: Append both handlers**

Append to `internal/handlers/uploads_chunked.go`:

```go
// statusResp is the JSON returned from GET /api/uploads/:upload_id.
type statusResp struct {
	Size           int64       `json:"size"`
	ReceivedRanges [][2]int64  `json:"received_ranges"`
	ExpiresAt      time.Time   `json:"expires_at"`
	Status         string      `json:"status"`
}

// GetUploadStatus handles GET /api/uploads/:upload_id.
func GetUploadStatus(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id := c.Param("upload_id")
	s, err := models.GetUploadSession(id)
	if err == sql.ErrNoRows || s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}
	if s.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	c.JSON(http.StatusOK, statusResp{
		Size:           s.Size,
		ReceivedRanges: s.ReceivedRanges,
		ExpiresAt:      s.ExpiresAt,
		Status:         s.Status,
	})
}

// CancelUpload handles DELETE /api/uploads/:upload_id.
func CancelUpload(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id := c.Param("upload_id")

	// Acquire the session lock to prevent racing with a concurrent PUT/finalize.
	lock := sessionLock(id)
	lock.Lock()
	defer lock.Unlock()

	s, err := models.GetUploadSession(id)
	if err == sql.ErrNoRows || s == nil {
		c.Status(http.StatusNoContent) // idempotent — already gone
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}
	if s.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	_ = storage.DeletePartial(id)
	_ = models.DeleteUploadSession(id)
	sessionLocks.Delete(id)
	c.Status(http.StatusNoContent)
}
```

- [ ] **Step 2: Wire temporary routes**

In `internal/server/server.go`, below the chunk route:

```go
api.GET("/uploads/:upload_id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(),
	middleware.RateLimit(600, 3600), handlers.GetUploadStatus)
api.DELETE("/uploads/:upload_id", middleware.AuthRequired(), handlers.RequireVerifiedEmail(),
	middleware.RateLimit(60, 3600), handlers.CancelUpload)
```

- [ ] **Step 3: Build + manual test**

```bash
go build -o playmore && ./playmore --port 18080 --data /tmp/playmore-test-data &
sleep 1

# Re-init (UID will differ from Task 5)
UID=$(curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"filename":"game.zip","size":100,"kind":"new_game","metadata":{"title":"T","genre":"action"}}' \
  | grep -o '"upload_id":"[^"]*"' | cut -d'"' -f4)

# Write a chunk at offset 20
printf 'XXXXXXXXXX' | curl -s -X PUT --data-binary @- \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/octet-stream" \
  "localhost:18080/api/uploads/$UID/chunks?offset=20"

# Status — expect [[20,30]] gap structure
curl -s -H "Authorization: Bearer $KEY" "localhost:18080/api/uploads/$UID"
# Expected: {"size":100,"received_ranges":[[20,30]],"expires_at":"...","status":"open"}

# Cancel
curl -s -X DELETE -H "Authorization: Bearer $KEY" "localhost:18080/api/uploads/$UID" -w "%{http_code}\n"
# Expected: 204

# Status now 404
curl -s -H "Authorization: Bearer $KEY" "localhost:18080/api/uploads/$UID"
# Expected: {"error":"not found"}

# Partial file gone
ls /tmp/playmore-test-data/uploads/.partial/$UID.bin 2>&1
# Expected: cannot access ... No such file
```

Kill: `kill %1`

- [ ] **Step 4: Commit**

```bash
git add internal/handlers/uploads_chunked.go internal/server/server.go
git commit -m "feat: chunked upload — status + cancel handlers"
```

---

### Task 7: `FinalizeUpload` handler

**Files:**
- Modify: `internal/handlers/uploads_chunked.go`

- [ ] **Step 1: Append the handler**

Append to `internal/handlers/uploads_chunked.go`:

```go
// finalizeReq is the JSON body of POST /api/uploads/:upload_id/finalize.
type finalizeReq struct {
	SHA256 string `json:"sha256,omitempty"`
}

// finalizeResp is the success body for kind=new_game.
type finalizeResp struct {
	GameID string `json:"game_id"`
}

// FinalizeUpload handles POST /api/uploads/:upload_id/finalize.
func FinalizeUpload(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id := c.Param("upload_id")

	var req finalizeReq
	_ = c.ShouldBindJSON(&req) // body is optional

	// Acquire session lock to prevent races with PUT/cancel.
	lock := sessionLock(id)
	lock.Lock()
	defer func() {
		lock.Unlock()
		// Note: don't sessionLocks.Delete(id) yet — keep around briefly so a
		// late retry doesn't create a new lock and race; GC handles cleanup.
	}()

	s, err := models.GetUploadSession(id)
	if err == sql.ErrNoRows || s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}
	if s.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if !models.IsComplete(s.ReceivedRanges, s.Size) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "upload incomplete"})
		return
	}

	// Atomic open → finalizing
	won, err := models.MarkFinalizing(id, req.SHA256)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "state update failed"})
		return
	}
	if !won {
		c.JSON(http.StatusConflict, gin.H{"error": "session not open"})
		return
	}

	// On any error from here, mark failed + clean up.
	failFinalize := func(code int, msg string) {
		_ = models.MarkStatus(id, "failed")
		_ = storage.DeletePartial(id)
		c.JSON(code, gin.H{"error": msg})
	}

	// Optional sha256 verification
	if req.SHA256 != "" {
		f, err := storage.OpenPartial(id)
		if err != nil {
			failFinalize(http.StatusInternalServerError, "open partial failed")
			return
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			failFinalize(http.StatusInternalServerError, "hash failed")
			return
		}
		f.Close()
		got := hex.EncodeToString(h.Sum(nil))
		if got != strings.ToLower(req.SHA256) {
			failFinalize(http.StatusBadRequest, "sha256 mismatch")
			return
		}
	}

	// Open the partial for ZIP extraction (it's also a ReaderAt)
	f, err := storage.OpenPartial(id)
	if err != nil {
		failFinalize(http.StatusInternalServerError, "open partial failed")
		return
	}
	defer f.Close()

	var meta gameMetadata
	if s.Kind == "new_game" {
		if err := json.Unmarshal([]byte(s.MetadataJSON), &meta); err != nil {
			failFinalize(http.StatusInternalServerError, "metadata decode failed")
			return
		}
	}

	// Decide game target: existing (reupload) or new (created here).
	var targetGameID string
	if s.Kind == "reupload" {
		targetGameID = s.GameID.String
		// Wipe old game dir before extracting to avoid leftover files
		_ = storage.DeleteGameFiles(targetGameID)
	} else {
		// new_game — create the row
		price := 0.0
		g, err := models.CreateGame(meta.Title, meta.Genre, meta.Description, user.ID, price, meta.Tags, meta.IsWebGPU)
		if err != nil || g == nil {
			failFinalize(http.StatusInternalServerError, "create game failed")
			return
		}
		targetGameID = g.ID
	}

	// Extract or place file
	lowerName := strings.ToLower(s.Filename)
	var entryFile string
	switch {
	case strings.HasSuffix(lowerName, ".zip"):
		ef, err := storage.ExtractZipFromReader(targetGameID, f, s.Size)
		if err != nil {
			if s.Kind == "new_game" {
				_ = models.DeleteGame(targetGameID)
			}
			failFinalize(http.StatusBadRequest, "invalid game file")
			return
		}
		entryFile = ef
	case strings.HasSuffix(lowerName, ".html"), strings.HasSuffix(lowerName, ".htm"):
		if _, err := f.Seek(0, 0); err != nil {
			if s.Kind == "new_game" {
				_ = models.DeleteGame(targetGameID)
			}
			failFinalize(http.StatusInternalServerError, "seek partial failed")
			return
		}
		htmlData, _ := io.ReadAll(f)
		if err := storage.SaveGameFile(targetGameID, s.Filename, htmlData); err != nil {
			if s.Kind == "new_game" {
				_ = models.DeleteGame(targetGameID)
			}
			failFinalize(http.StatusInternalServerError, "save file failed")
			return
		}
		entryFile = s.Filename
	default:
		if s.Kind == "new_game" {
			_ = models.DeleteGame(targetGameID)
		}
		failFinalize(http.StatusBadRequest, "game file must be .html, .htm, or .zip")
		return
	}

	if err := models.UpdateGameFiles(targetGameID, storage.GameDir(targetGameID), entryFile); err != nil {
		failFinalize(http.StatusInternalServerError, "update game files failed")
		return
	}

	// Success — clean up session + partial
	_ = models.MarkStatus(id, "done")
	_ = storage.DeletePartial(id)
	_ = models.DeleteUploadSession(id)

	if s.Kind == "new_game" {
		c.JSON(http.StatusOK, finalizeResp{GameID: targetGameID})
	} else {
		c.Status(http.StatusNoContent)
	}
}
```

Add to imports:
```go
"crypto/sha256"
"encoding/hex"
"strings"
```

Note: check actual function signatures in `internal/models/game.go` — `CreateGame`, `DeleteGame`, `UpdateGameFiles` may have different names or parameter orders (e.g., `(*Game).UpdateFiles(dir, entryFile)` instance method as seen in `games.go:188`). Adapt to match.

- [ ] **Step 2: Wire temporary finalize route**

In `internal/server/server.go`:

```go
api.POST("/uploads/:upload_id/finalize", middleware.AuthRequired(), handlers.RequireVerifiedEmail(),
	middleware.RateLimit(20, 3600), limitBody(1<<20), handlers.FinalizeUpload)
```

- [ ] **Step 3: Build + manual end-to-end test with a small ZIP**

```bash
# Create a small test game ZIP
mkdir -p /tmp/testgame && echo '<html><body>Hi</body></html>' > /tmp/testgame/index.html
(cd /tmp/testgame && zip -r /tmp/testgame.zip .)
SIZE=$(stat -c%s /tmp/testgame.zip)
SHA=$(sha256sum /tmp/testgame.zip | awk '{print $1}')

# Build & run
go build -o playmore && ./playmore --port 18080 --data /tmp/playmore-test-data &
sleep 1

# Init
UID=$(curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d "{\"filename\":\"test.zip\",\"size\":$SIZE,\"kind\":\"new_game\",\"metadata\":{\"title\":\"TestChunk\",\"genre\":\"action\"}}" \
  | grep -o '"upload_id":"[^"]*"' | cut -d'"' -f4)

# Upload the whole file as one chunk (it's small)
curl -s -X PUT --data-binary @/tmp/testgame.zip \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/octet-stream" \
  "localhost:18080/api/uploads/$UID/chunks?offset=0"
# Expected: {"received_bytes":<SIZE>}

# Finalize with sha
GAME=$(curl -s -X POST localhost:18080/api/uploads/$UID/finalize \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d "{\"sha256\":\"$SHA\"}" \
  | grep -o '"game_id":"[^"]*"' | cut -d'"' -f4)
echo "game_id=$GAME"

# Verify game file got extracted
ls /tmp/playmore-test-data/games/$GAME/
# Expected: index.html

# Negative: finalize with wrong sha
UID2=$(curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d "{\"filename\":\"test.zip\",\"size\":$SIZE,\"kind\":\"new_game\",\"metadata\":{\"title\":\"T2\",\"genre\":\"action\"}}" \
  | grep -o '"upload_id":"[^"]*"' | cut -d'"' -f4)
curl -s -X PUT --data-binary @/tmp/testgame.zip \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/octet-stream" \
  "localhost:18080/api/uploads/$UID2/chunks?offset=0" > /dev/null
curl -s -X POST localhost:18080/api/uploads/$UID2/finalize \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"sha256":"deadbeef"}'
# Expected: {"error":"sha256 mismatch"}
sqlite3 /tmp/playmore-test-data/playmore.db "SELECT status FROM upload_sessions WHERE id='$UID2'"
# Expected: (no row — finalize cleanup ran) OR `failed` depending on order
```

Kill: `kill %1`

- [ ] **Step 4: Commit**

```bash
git add internal/handlers/uploads_chunked.go internal/server/server.go
git commit -m "feat: chunked upload — FinalizeUpload handler with sha verification"
```

---

### Task 8: GC goroutine

**Files:**
- Create: `internal/storage/partial_gc.go`
- Modify: `main.go`

- [ ] **Step 1: Create the GC file**

Create `internal/storage/partial_gc.go`:

```go
package storage

import (
	"context"
	"log"
	"time"

	"github.com/yusufkaraaslan/play-more/internal/models"
)

// PartialGCInterval is how often the sweep runs.
const PartialGCInterval = 10 * time.Minute

// StartPartialGC starts a background goroutine that periodically deletes
// expired upload_sessions (status='open' AND expires_at < now) and orphan
// partial files (files with no matching session row).
func StartPartialGC(ctx context.Context) {
	go func() {
		t := time.NewTicker(PartialGCInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sweepPartials()
			}
		}
	}()
}

func sweepPartials() {
	// 1. Expire stale sessions
	ids, err := models.ExpiredOpenSessionIDs(time.Now().UTC())
	if err != nil {
		log.Printf("partial_gc: expired query: %v", err)
	}
	for _, id := range ids {
		if err := DeletePartial(id); err != nil {
			log.Printf("partial_gc: delete partial %s: %v", id, err)
		}
		if err := models.DeleteUploadSession(id); err != nil {
			log.Printf("partial_gc: delete session %s: %v", id, err)
		}
	}

	// 2. Orphan file sweep — files with no session row
	knownIDs, err := models.AllSessionIDs()
	if err != nil {
		log.Printf("partial_gc: list sessions: %v", err)
		return
	}
	fileIDs, err := ListPartialIDs()
	if err != nil {
		log.Printf("partial_gc: list partials: %v", err)
		return
	}
	for _, id := range fileIDs {
		if _, ok := knownIDs[id]; ok {
			continue
		}
		if err := DeletePartial(id); err != nil {
			log.Printf("partial_gc: delete orphan partial %s: %v", id, err)
		}
	}
}
```

- [ ] **Step 2: Wire from main.go**

In `main.go`, find where `middleware.StartRateLimitCleanup()` or `middleware.StartAnalyticsWriter()` is called. Add:

```go
storage.StartPartialGC(context.Background())
```

(If `context` isn't already imported in `main.go`, add `"context"` to imports.)

- [ ] **Step 3: Build**

Run: `go build -o playmore`
Expected: success.

- [ ] **Step 4: Manual GC test**

```bash
./playmore --port 18080 --data /tmp/playmore-test-data &
sleep 1

# Init a session
UID=$(curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"filename":"x.zip","size":100,"kind":"new_game","metadata":{"title":"T","genre":"action"}}' \
  | grep -o '"upload_id":"[^"]*"' | cut -d'"' -f4)
ls /tmp/playmore-test-data/uploads/.partial/$UID.bin   # exists

# Artificially expire
sqlite3 /tmp/playmore-test-data/playmore.db \
  "UPDATE upload_sessions SET expires_at = datetime('now','-1 hour') WHERE id='$UID'"

# Wait until next tick OR shorten the interval temporarily. For testing, just
# kill the server and run the sweep manually by writing a one-off Go program,
# OR rebuild with PartialGCInterval temporarily set to 5 * time.Second.

# Simpler manual approach: shorten interval, rebuild, run for 10s
# 1) edit partial_gc.go: change PartialGCInterval to 5 * time.Second
# 2) go build -o playmore
# 3) ./playmore ... ; sleep 10
# 4) Verify cleanup

ls /tmp/playmore-test-data/uploads/.partial/$UID.bin 2>&1
# Expected: No such file
sqlite3 /tmp/playmore-test-data/playmore.db "SELECT id FROM upload_sessions WHERE id='$UID'"
# Expected: (empty)

# Orphan-file sweep
touch /tmp/playmore-test-data/uploads/.partial/orphan-xxx.bin
sleep 10
ls /tmp/playmore-test-data/uploads/.partial/orphan-xxx.bin 2>&1
# Expected: No such file
```

**Important:** revert `PartialGCInterval` back to `10 * time.Minute` before committing.

Kill: `kill %1`

- [ ] **Step 5: Commit**

```bash
git add internal/storage/partial_gc.go main.go
git commit -m "feat: chunked upload — GC goroutine for expired sessions + orphan files"
```

---

### Task 9: Move routes to a dedicated group with final wiring

**Files:**
- Modify: `internal/server/server.go`

- [ ] **Step 1: Replace temporary scattered route registrations with a dedicated group**

Find all 5 `api.*("/uploads/...", ...)` lines added in Tasks 4-7 and replace with a single coherent block (placed near the other upload routes, around line 278 where `/api/upload/image` lives):

```go
// Chunked upload pipeline — see docs/superpowers/specs/2026-05-21-chunked-upload-design.md
chunkPutCap := int64((8 << 20) + (1 << 20)) // 8 MiB chunk + 1 MiB headroom = 9 MiB
api.POST(  "/uploads/init",                middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20,   3600), limitBody(1<<20),       handlers.InitUpload)
api.PUT(   "/uploads/:upload_id/chunks",   middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(2000, 3600), limitBody(chunkPutCap), handlers.PutChunk)
api.GET(   "/uploads/:upload_id",          middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(600,  3600),                          handlers.GetUploadStatus)
api.POST(  "/uploads/:upload_id/finalize", middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(20,   3600), limitBody(1<<20),       handlers.FinalizeUpload)
api.DELETE("/uploads/:upload_id",          middleware.AuthRequired(), handlers.RequireVerifiedEmail(), middleware.RateLimit(60,   3600),                          handlers.CancelUpload)
```

- [ ] **Step 2: Build + sanity-check the routes still work**

```bash
go build -o playmore && ./playmore --port 18080 --data /tmp/playmore-test-data &
sleep 1
curl -s -X POST localhost:18080/api/uploads/init \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"filename":"x.zip","size":100,"kind":"new_game","metadata":{"title":"T","genre":"action"}}'
# Expected: {"upload_id":"...",...}
kill %1
```

- [ ] **Step 3: Commit**

```bash
git add internal/server/server.go
git commit -m "feat: chunked upload — consolidate route registrations"
```

---

### Task 10: SPA — `uploadGameChunked` helper + progress UI

**Files:**
- Modify: `frontend/index.html`

- [ ] **Step 1: Find a good place to add the helper**

Add the helper near the existing `api()` helper. Search for `function api(` in `frontend/index.html` and add `uploadGameChunked` immediately after it.

- [ ] **Step 2: Add the helper**

```js
const CHUNKED_THRESHOLD = 64 * 1024 * 1024; // 64 MiB

// missingRanges computes [start, end) gaps in [0, size) not covered by `ranges`.
// Shared between upload start (resume detection) and the resume-on-load banner.
function missingRanges(ranges, size) {
    const out = []; let cur = 0;
    for (const [s, e] of ranges) {
        if (s > cur) out.push([cur, s]);
        if (e > cur) cur = e;
    }
    if (cur < size) out.push([cur, size]);
    return out;
}

async function putChunkWithRetry(uploadId, offset, slice) {
    const url = `/api/uploads/${uploadId}/chunks?offset=${offset}`;
    let lastErr;
    for (let attempt = 0; attempt < 4; attempt++) {
        try {
            const res = await fetch(url, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/octet-stream' },
                body: slice,
                credentials: 'same-origin',
            });
            if (!res.ok) {
                const body = await res.json().catch(() => ({}));
                throw new Error(body.error || `HTTP ${res.status}`);
            }
            return await res.json();
        } catch (e) {
            lastErr = e;
            if (attempt < 3) await new Promise(r => setTimeout(r, 1000 * (1 << attempt)));
        }
    }
    throw lastErr;
}

async function uploadGameChunked(file, { kind, gameId, metadata }, onProgress) {
    const initBody = { filename: file.name, size: file.size, kind };
    if (kind === 'reupload') initBody.game_id = gameId;
    if (kind === 'new_game') initBody.metadata = metadata;

    const init = await api('/api/uploads/init', {
        method: 'POST',
        body: JSON.stringify(initBody),
    });
    const { upload_id, chunk_size } = init;
    sessionStorage.setItem('playmore_upload_id', upload_id);

    // Check for partial state (resume case)
    let receivedRanges = [];
    try {
        const status = await api(`/api/uploads/${upload_id}`);
        receivedRanges = status.received_ranges || [];
    } catch (e) { /* fresh upload */ }

    // Walk the missing ranges in chunk_size steps
    const toSend = missingRanges(receivedRanges, file.size);
    let sentBytes = receivedRanges.reduce((acc, [s, e]) => acc + Math.max(0, e - s), 0);
    for (const [start, end] of toSend) {
        for (let offset = start; offset < end; offset += chunk_size) {
            const sliceEnd = Math.min(offset + chunk_size, end);
            const slice = file.slice(offset, sliceEnd);
            await putChunkWithRetry(upload_id, offset, slice);
            sentBytes += (sliceEnd - offset);
            if (onProgress) onProgress(sentBytes / file.size);
        }
    }

    const finalized = await api(`/api/uploads/${upload_id}/finalize`, {
        method: 'POST',
        body: JSON.stringify({}), // SPA omits sha256
    });
    sessionStorage.removeItem('playmore_upload_id');
    return finalized;
}
```

- [ ] **Step 3: Build + run + smoke-check with a 70 MiB synthetic file**

```bash
go build -o playmore && ./playmore --port 18080 --data /tmp/playmore-test-data &
sleep 1
# Create a 70 MiB junk ZIP (just for size — we'll cancel before finalize, no real game)
head -c $((70*1024*1024)) /dev/urandom > /tmp/big.zip

# Open localhost:18080 in browser, log in as the seeded admin, open DevTools console:
# > const f = new File([await (await fetch('http://localhost:18080/big.zip')).blob()], 'big.zip')
# (or use a regular file input)
# > await uploadGameChunked(f, { kind: 'new_game', metadata: { title:'T', genre:'action' } }, p => console.log(p))
# Expected: ~9 network requests in DevTools (1 init + 1 status + 8 PUTs), progress 0..1, then finalize fails because random bytes aren't a valid ZIP. OK — what we're testing is that the chunked path executed.
```

- [ ] **Step 4: Commit**

```bash
git add frontend/index.html
git commit -m "feat: chunked upload — SPA uploadGameChunked helper"
```

---

### Task 11: SPA — branch new-game upload at 64 MiB

**Files:**
- Modify: `frontend/index.html` (around line 1275-1290 — the existing `formData.append('game_file', pendingFile)` block)

- [ ] **Step 1: Find the new-game upload code**

Run: `grep -n "game_file', pendingFile\|/api/games', { method: 'POST'" frontend/index.html`

This is the form submit handler for new-game upload. It probably looks roughly like:

```js
const formData = new FormData();
formData.append('game_file', pendingFile);
formData.append('title', titleInput.value);
// ... etc
const res = await fetch('/api/games', { method: 'POST', body: formData, credentials: 'same-origin' });
const game = await res.json();
// then handle cover/screenshots
```

- [ ] **Step 2: Add the threshold branch**

Wrap the existing code in a size check:

```js
let createdGame;
if (pendingFile.size > CHUNKED_THRESHOLD) {
    // Chunked path
    const metadata = {
        title: titleInput.value,
        genre: genreInput.value,
        description: descInput.value,
        tags: tagsInput.value.split(',').map(s => s.trim()).filter(Boolean),
        is_webgpu: webgpuInput.checked,
    };
    showUploadProgress(0);
    const finalized = await uploadGameChunked(
        pendingFile,
        { kind: 'new_game', metadata },
        p => showUploadProgress(p)
    );
    createdGame = { id: finalized.game_id };
    // After finalize, upload cover + screenshots out-of-band against /api/games/<id>
    if (coverInput.files[0]) {
        const coverFD = new FormData();
        coverFD.append('image', coverInput.files[0]);
        const coverRes = await fetch('/api/upload/image', { method: 'POST', body: coverFD, credentials: 'same-origin' });
        const { url } = await coverRes.json();
        await api(`/api/games/${createdGame.id}`, { method: 'PUT', body: JSON.stringify({ cover_url: url }) });
    }
    // Same pattern for screenshots if present — see existing reupload flow at line ~2639
} else {
    // Existing single-shot path — unchanged
    const formData = new FormData();
    formData.append('game_file', pendingFile);
    // ... (existing code unchanged)
    const res = await fetch('/api/games', { method: 'POST', body: formData, credentials: 'same-origin' });
    createdGame = await res.json();
}
// Navigate to the created game (existing code)
navigate(`game/${createdGame.id}`);
```

(Adapt variable names — `titleInput`, `genreInput` etc — to whatever the current code uses. They may be `document.getElementById('title').value` or similar.)

- [ ] **Step 3: Add the progress UI helper**

If `showUploadProgress` doesn't exist, add it near other UI helpers:

```js
function showUploadProgress(fraction) {
    let el = document.getElementById('upload-progress');
    if (!el) {
        // Inject into the upload modal — adapt selector to current modal markup
        const modal = document.querySelector('.upload-modal-content') || document.querySelector('.modal-content');
        if (!modal) return;
        modal.insertAdjacentHTML('beforeend',
            '<div id="upload-progress-wrap" style="margin-top:1em;">' +
            '  <div style="background:#222;border-radius:4px;overflow:hidden;height:8px;">' +
            '    <div id="upload-progress" style="background:#4caf50;height:100%;width:0%;transition:width .2s"></div>' +
            '  </div>' +
            '  <div id="upload-progress-label" style="text-align:center;font-size:.85em;margin-top:.25em">0%</div>' +
            '</div>');
        el = document.getElementById('upload-progress');
    }
    const pct = Math.round(fraction * 100);
    el.style.width = pct + '%';
    const label = document.getElementById('upload-progress-label');
    if (label) label.textContent = pct + '%';
    if (fraction >= 1) {
        setTimeout(() => {
            const w = document.getElementById('upload-progress-wrap');
            if (w) w.remove();
        }, 1000);
    }
}
```

- [ ] **Step 4: Manual test**

Build, run, log in, upload a small (<64 MiB) HTML file → DevTools shows ONE `POST /api/games` multipart request. Upload a >64 MiB ZIP → DevTools shows `init` + multiple `PUT chunks` + `finalize` sequence + cover image POST. Game appears with cover.

- [ ] **Step 5: Commit**

```bash
git add frontend/index.html
git commit -m "feat: chunked upload — SPA new-game branching at 64 MiB"
```

---

### Task 12: SPA — branch reupload at 64 MiB

**Files:**
- Modify: `frontend/index.html` (around line 2620 — existing `/api/games/<id>/reupload` flow)

- [ ] **Step 1: Find the reupload code**

Run: `grep -n "/reupload', { method" frontend/index.html`

It looks like:

```js
const formData = new FormData();
formData.append('game_file', fileInput.files[0]);
const res = await fetch('/api/games/' + id + '/reupload', { method: 'POST', body: formData, credentials: 'same-origin' });
```

- [ ] **Step 2: Add the threshold branch**

```js
const file = fileInput.files[0];
if (file.size > CHUNKED_THRESHOLD) {
    showUploadProgress(0);
    await uploadGameChunked(
        file,
        { kind: 'reupload', gameId: id },
        p => showUploadProgress(p)
    );
} else {
    const formData = new FormData();
    formData.append('game_file', file);
    const res = await fetch('/api/games/' + id + '/reupload', { method: 'POST', body: formData, credentials: 'same-origin' });
    if (!res.ok) throw new Error(await res.text());
}
```

- [ ] **Step 3: Manual test**

Upload >64 MiB reupload via SPA → finalize returns 204, game files replaced.

- [ ] **Step 4: Commit**

```bash
git add frontend/index.html
git commit -m "feat: chunked upload — SPA reupload branching at 64 MiB"
```

---

### Task 13: SPA — resume on retry

**Files:**
- Modify: `frontend/index.html`

- [ ] **Step 1: Add resume-detection on app start**

Add this near the bottom of the script block, after the initial route setup:

```js
// If we have a stashed upload_id from a previous session, offer to resume
(function checkResumeOnLoad() {
    const id = sessionStorage.getItem('playmore_upload_id');
    if (!id) return;
    api(`/api/uploads/${id}`).then(status => {
        if (status.status !== 'open') {
            sessionStorage.removeItem('playmore_upload_id');
            return;
        }
        const bar = document.createElement('div');
        bar.style.cssText = 'position:fixed;top:0;left:0;right:0;background:#234;color:#fff;padding:.6em;text-align:center;z-index:9999';
        bar.innerHTML = 'You have an interrupted upload. <button id="resume-btn" style="margin-left:1em">Re-select file & resume</button> <button id="resume-dismiss" style="margin-left:.5em">Dismiss</button>';
        document.body.prepend(bar);
        document.getElementById('resume-dismiss').onclick = () => { sessionStorage.removeItem('playmore_upload_id'); bar.remove(); };
        document.getElementById('resume-btn').onclick = () => {
            const input = document.createElement('input');
            input.type = 'file';
            input.accept = '.zip,.html,.htm';
            input.onchange = async () => {
                const file = input.files[0];
                if (!file || file.size !== status.size) {
                    alert('File size doesn\'t match the interrupted upload — cancel the old session and start fresh.');
                    return;
                }
                showUploadProgress(0);
                try {
                    // The existing helper handles resume because it queries status first
                    // and only sends missing ranges. But we need to point it at the
                    // EXISTING upload_id, not init a new session. Use a slimmer variant:
                    const chunk_size = 8 * 1024 * 1024;
                    const toSend = missingRanges(status.received_ranges || [], file.size);
                    let sentBytes = (status.received_ranges || []).reduce((a, [s, e]) => a + Math.max(0, e - s), 0);
                    for (const [start, end] of toSend) {
                        for (let offset = start; offset < end; offset += chunk_size) {
                            const sliceEnd = Math.min(offset + chunk_size, end);
                            await putChunkWithRetry(id, offset, file.slice(offset, sliceEnd));
                            sentBytes += (sliceEnd - offset);
                            showUploadProgress(sentBytes / file.size);
                        }
                    }
                    await api(`/api/uploads/${id}/finalize`, { method: 'POST', body: JSON.stringify({}) });
                    sessionStorage.removeItem('playmore_upload_id');
                    bar.remove();
                    alert('Upload resumed and finalized.');
                    location.reload();
                } catch (e) {
                    alert('Resume failed: ' + e.message);
                }
            };
            input.click();
        };
    }).catch(() => sessionStorage.removeItem('playmore_upload_id'));
})();
```

- [ ] **Step 2: Manual test**

1. Start a chunked upload via the new-game form (>64 MiB ZIP). Mid-upload (~50%), close the tab.
2. Re-open the app. A banner should appear at top: "You have an interrupted upload."
3. Click "Re-select file & resume", choose the same ZIP.
4. Progress should pick up from ~50%, not 0%. Verify in DevTools: `GET /api/uploads/:id` returns partial ranges, then only the missing chunks are PUT.
5. Finalize succeeds, game appears.

- [ ] **Step 3: Commit**

```bash
git add frontend/index.html
git commit -m "feat: chunked upload — SPA resume banner on app load"
```

---

### Task 14: CLI — `chunked_push` function

**Files:**
- Modify: `internal/handlers/playmore-deploy.sh`

- [ ] **Step 1: Add helpers near the top of the script**

After the existing `json_val_raw` helper (around line 114):

```bash
file_size() {
    # GNU vs BSD stat
    stat -c%s "$1" 2>/dev/null || stat -f%z "$1"
}

file_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}
```

- [ ] **Step 2: Add the `chunked_push` function**

After `cmd_push`'s closing brace (around line 233):

```bash
# chunked_push uploads $file via /api/uploads/init|chunks|finalize when the file
# is larger than the single-shot threshold (called from cmd_push).
#
# Args: $1=file  $2=size  $3=sha256  $4=kind (new_game|reupload)
#       For kind=new_game: env vars TITLE, GENRE, DESC, TAGS, IS_WEBGPU
#       For kind=reupload: env var TARGET_GAME_ID
chunked_push() {
    local file="$1" size="$2" sha="$3" kind="$4"
    local upload_id chunk_size

    # If we have a stashed UPLOAD_ID from a prior run, try to resume against it.
    local resume_id="${UPLOAD_ID:-}"
    if [[ -n "$resume_id" ]]; then
        local status
        status=$(api_call GET "/api/uploads/$resume_id" 2>/dev/null) || resume_id=""
        if [[ -n "$resume_id" ]]; then
            local declared_size
            declared_size=$(json_val_raw "$status" "size" | tr -d ' ')
            if [[ "$declared_size" == "$size" ]]; then
                upload_id="$resume_id"
                chunk_size=$((8*1024*1024))
                info "Resuming previous upload $upload_id"
            else
                warn "Stashed UPLOAD_ID doesn't match this file's size — starting fresh"
                resume_id=""
            fi
        fi
    fi

    if [[ -z "$upload_id" ]]; then
        local init_body
        if [[ "$kind" == "new_game" ]]; then
            init_body=$(printf '{"filename":"%s","size":%s,"kind":"new_game","metadata":{"title":"%s","genre":"%s","description":"%s","tags":[%s],"is_webgpu":%s}}' \
                "$(basename "$file")" "$size" "${TITLE//\"/\\\"}" "${GENRE//\"/\\\"}" "${DESC//\"/\\\"}" \
                "$(printf '"%s",' ${TAGS//,/ } | sed 's/,$//')" "${IS_WEBGPU:-false}")
        else
            init_body=$(printf '{"filename":"%s","size":%s,"kind":"reupload","game_id":"%s"}' \
                "$(basename "$file")" "$size" "$TARGET_GAME_ID")
        fi
        local init
        init=$(api_call POST "/api/uploads/init" -H "Content-Type: application/json" -d "$init_body") || die "init failed"
        upload_id=$(json_val "$init" "upload_id")
        chunk_size=$(json_val_raw "$init" "chunk_size" | tr -d ' ')
        UPLOAD_ID="$upload_id"
        save_config
    fi

    # Determine missing offsets if resuming
    local status received_to=0
    if [[ -n "$resume_id" ]]; then
        status=$(api_call GET "/api/uploads/$upload_id")
        received_to=$(echo "$status" | grep -o '"received_ranges":\[\[0,[0-9]*\]\]' | grep -o '[0-9]*' | tail -1)
        # Note: this simple parser assumes the (common) single contiguous-from-zero range case.
        # For complex gap recovery, the SPA path is more robust; CLI sequential uploads almost
        # always have contiguous-from-zero received_ranges.
        [[ -z "$received_to" ]] && received_to=0
    fi

    local offset=$received_to
    local total_chunks=$(( (size + chunk_size - 1) / chunk_size ))
    local current_chunk=$(( received_to / chunk_size ))
    while [[ $offset -lt $size ]]; do
        local remaining=$((size - offset))
        local this_chunk=$chunk_size
        [[ $remaining -lt $chunk_size ]] && this_chunk=$remaining

        local skip=$((offset / chunk_size))
        current_chunk=$((current_chunk + 1))
        printf "  [%2d/%2d] uploading chunk @ offset %d (%d bytes)\n" \
            "$current_chunk" "$total_chunks" "$offset" "$this_chunk"

        local attempt
        local ok=false
        for attempt in 0 1 2 3; do
            if [[ $attempt -gt 0 ]]; then
                sleep $((1 << (attempt - 1)))
                warn "  retry $attempt"
            fi
            if [[ $remaining -ge $chunk_size ]]; then
                if dd if="$file" bs="$chunk_size" skip="$skip" count=1 status=none 2>/dev/null \
                    | curl --fail -s -X PUT --data-binary @- \
                        -H "Authorization: Bearer $API_KEY" \
                        -H "Content-Type: application/octet-stream" \
                        "${SERVER}/api/uploads/${upload_id}/chunks?offset=${offset}" > /dev/null
                then
                    ok=true; break
                fi
            else
                # Last (partial) chunk — bs=1 is slow but only for the trailing remainder
                if dd if="$file" bs=1 skip="$offset" count="$this_chunk" status=none 2>/dev/null \
                    | curl --fail -s -X PUT --data-binary @- \
                        -H "Authorization: Bearer $API_KEY" \
                        -H "Content-Type: application/octet-stream" \
                        "${SERVER}/api/uploads/${upload_id}/chunks?offset=${offset}" > /dev/null
                then
                    ok=true; break
                fi
            fi
        done
        $ok || die "chunk upload failed after 4 attempts — re-run 'playmore-deploy push' to resume"

        offset=$((offset + this_chunk))
    done

    info "Finalizing..."
    local fin
    fin=$(api_call POST "/api/uploads/$upload_id/finalize" \
            -H "Content-Type: application/json" \
            -d "{\"sha256\":\"$sha\"}") || die "finalize failed"

    # Clear stashed UPLOAD_ID on success
    UPLOAD_ID=""
    save_config

    if [[ "$kind" == "new_game" ]]; then
        local new_id
        new_id=$(json_val "$fin" "game_id")
        if [[ -n "$new_id" ]]; then
            GAME_ID="$new_id"
            save_config
            success "Game uploaded! ID: $GAME_ID"
        fi
    else
        success "Game files updated!"
    fi
}
```

- [ ] **Step 3: Update `save_config` to include `UPLOAD_ID`**

Find `save_config` (around line 78) and update:

```bash
save_config() {
    cat > "$CONFIG_FILE" <<EOF
SERVER='$SERVER'
API_KEY='$API_KEY'
GAME_ID='$GAME_ID'
UPLOAD_ID='${UPLOAD_ID:-}'
EOF
    success "Config saved to $CONFIG_FILE"
}
```

And `load_config` (around line 61) to read it:

```bash
load_config() {
    SERVER="" API_KEY="" GAME_ID="" UPLOAD_ID=""
    local file=""
    if [[ -f "$CONFIG_FILE" ]]; then file="$CONFIG_FILE"
    elif [[ -f "$GLOBAL_CONFIG" ]]; then file="$GLOBAL_CONFIG"
    fi
    [[ -n "$file" ]] || return
    while IFS='=' read -r key value; do
        value="${value#\'}" ; value="${value%\'}"
        case "$key" in
            SERVER)    SERVER="$value" ;;
            API_KEY)   API_KEY="$value" ;;
            GAME_ID)   GAME_ID="$value" ;;
            UPLOAD_ID) UPLOAD_ID="$value" ;;
        esac
    done < "$file"
}
```

- [ ] **Step 4: Commit (CLI not yet wired into cmd_push — that's Task 15)**

```bash
git add internal/handlers/playmore-deploy.sh
git commit -m "feat: chunked upload — CLI chunked_push helper + UPLOAD_ID config"
```

---

### Task 15: CLI — branch `cmd_push` at 64 MiB

**Files:**
- Modify: `internal/handlers/playmore-deploy.sh` (around line 152, `cmd_push`)

- [ ] **Step 1: Add branching to cmd_push**

Find the section in `cmd_push` that runs after `[[ -f "$file" ]] || die "File not found: $file"` (around line 194). Insert the threshold check:

```bash
local size sha
size=$(file_size "$file")
local THRESHOLD=$((64*1024*1024))

if [[ -n "$GAME_ID" ]]; then
    if [[ $size -gt $THRESHOLD ]]; then
        info "Re-uploading via chunked pipeline ($size bytes)..."
        sha=$(file_sha256 "$file")
        TARGET_GAME_ID="$GAME_ID" \
            chunked_push "$file" "$size" "$sha" "reupload"
    else
        # Existing single-shot reupload path (unchanged)
        info "Re-uploading files to game $GAME_ID..."
        local result
        result=$(api_call POST "/api/games/$GAME_ID/reupload" \
            -F "game_file=@$file") || exit 1
        success "Game files updated!"
    fi
else
    [[ -n "$title" ]] || read -rp "Game title: " title
    [[ -n "$genre" ]] || read -rp "Genre (action/adventure/rpg/strategy/puzzle/racing/horror/experimental): " genre
    [[ -n "$title" ]] || die "Title is required"
    [[ -n "$genre" ]] || die "Genre is required"

    if [[ $size -gt $THRESHOLD ]]; then
        info "Uploading new game via chunked pipeline: $title ($size bytes)..."
        sha=$(file_sha256 "$file")
        TITLE="$title" GENRE="$genre" DESC="$desc" TAGS="$tags" IS_WEBGPU="$webgpu" \
            chunked_push "$file" "$size" "$sha" "new_game"
        # Cover image (if specified) — out-of-band after finalize
        if [[ -n "$cover" && -f "$cover" ]]; then
            info "Uploading cover image..."
            local cover_res cover_url
            cover_res=$(api_call POST "/api/upload/image" -F "image=@$cover") || warn "cover upload failed"
            cover_url=$(json_val "$cover_res" "url")
            [[ -n "$cover_url" ]] && api_call PUT "/api/games/$GAME_ID" \
                -H "Content-Type: application/json" \
                -d "{\"cover_url\":\"$cover_url\"}" > /dev/null
        fi
    else
        # Existing single-shot new-game path (unchanged)
        info "Uploading new game: $title..."
        local curl_args=(-F "game_file=@$file" -F "title=$title" -F "genre=$genre" -F "is_webgpu=$webgpu")
        [[ -n "$desc" ]] && curl_args+=(-F "description=$desc")
        [[ -n "$tags" ]] && curl_args+=(-F "tags=$tags")
        [[ -n "$cover" && -f "$cover" ]] && curl_args+=(-F "cover=@$cover")

        local result
        result=$(api_call POST "/api/games" "${curl_args[@]}") || exit 1

        local new_id
        new_id=$(json_val "$result" "id")
        if [[ -n "$new_id" ]]; then
            GAME_ID="$new_id"
            save_config
            success "Game uploaded! ID: $GAME_ID"
        else
            success "Game uploaded!"
        fi
    fi
fi

echo ""
echo "  View: ${SERVER}/#game/${GAME_ID:-unknown}"
```

- [ ] **Step 2: Manual test — small file uses single-shot**

```bash
# Build & restart server, then deploy a small game
cd /tmp/testgame
playmore-deploy init --server localhost:18080 --key $KEY
playmore-deploy push --title TestSmall --genre puzzle
```

Watch the server log: should see ONE `POST /api/games` request, no `/api/uploads/*`.

- [ ] **Step 3: Manual test — large file uses chunked**

```bash
# Create a >64 MiB ZIP
head -c $((80*1024*1024)) /dev/urandom > /tmp/payload.bin
mkdir -p /tmp/biggame && mv /tmp/payload.bin /tmp/biggame/payload.bin
echo '<html><body>Big game</body></html>' > /tmp/biggame/index.html
(cd /tmp/biggame && zip -r /tmp/biggame.zip .)

cd /tmp/biggame
playmore-deploy push --file /tmp/biggame.zip --title TestBig --genre experimental
```

Watch server log: should see `POST /api/uploads/init`, multiple `PUT /api/uploads/.../chunks`, then `POST /api/uploads/.../finalize`. Game playable in browser.

- [ ] **Step 4: Commit**

```bash
git add internal/handlers/playmore-deploy.sh
git commit -m "feat: chunked upload — CLI cmd_push branches at 64 MiB"
```

---

### Task 16: CLI — resume on rerun

**Files:**
- already added in Tasks 14-15 (chunked_push consults `UPLOAD_ID` from config).

- [ ] **Step 1: Manual end-to-end resume test**

```bash
cd /tmp/biggame
# Start a fresh chunked upload; interrupt mid-stream
playmore-deploy push --file /tmp/biggame.zip --title TestResume --genre experimental &
PID=$!
sleep 3   # let a few chunks go up
kill $PID
cat .playmore
# Expected: UPLOAD_ID='<uuid>' present

# Resume by re-running the same command
playmore-deploy push --file /tmp/biggame.zip --title TestResume --genre experimental
# Expected: "Resuming previous upload <uuid>" + chunk progress continues
# Final: "Game uploaded! ID: ..." and UPLOAD_ID is cleared from .playmore
cat .playmore
# Expected: UPLOAD_ID='' (or no UPLOAD_ID line)
```

- [ ] **Step 2: Commit (no code change, just verification)**

If no code changed in this task, skip commit. If the manual test surfaced a bug, fix it and commit:

```bash
git add internal/handlers/playmore-deploy.sh
git commit -m "fix: chunked upload — CLI resume edge case"
```

---

### Task 17: Docs — `DEVELOPER.md` chunked uploads section

**Files:**
- Modify: `docs/DEVELOPER.md`

- [ ] **Step 1: Append the section**

Append to `docs/DEVELOPER.md`:

````markdown
## Chunked uploads

For files larger than 64 MiB (or behind a reverse proxy with a smaller body cap, like Cloudflare Free/Pro at 100 MiB), use the chunked upload pipeline instead of the single-shot `POST /api/games`.

### Endpoints

- `POST /api/uploads/init` — create an upload session
- `PUT /api/uploads/:upload_id/chunks?offset=N` — write bytes at a byte offset
- `GET /api/uploads/:upload_id` — check progress / find missing bytes (for resume)
- `POST /api/uploads/:upload_id/finalize` — assemble + extract + create or update the game
- `DELETE /api/uploads/:upload_id` — cancel and clean up

### Full curl example (new game)

```bash
FILE=/path/to/game.zip
SIZE=$(stat -c%s "$FILE" 2>/dev/null || stat -f%z "$FILE")
SHA=$(sha256sum "$FILE" | awk '{print $1}')

# 1. Init
INIT=$(curl -s -X POST "$SERVER/api/uploads/init" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d "{\"filename\":\"game.zip\",\"size\":$SIZE,\"kind\":\"new_game\",
       \"metadata\":{\"title\":\"My Game\",\"genre\":\"action\",
                     \"description\":\"Hi\",\"tags\":[\"foo\"],\"is_webgpu\":false}}")
UPLOAD_ID=$(echo "$INIT" | jq -r .upload_id)
CHUNK=$(echo "$INIT" | jq -r .chunk_size)

# 2. PUT chunks
OFFSET=0
while [ $OFFSET -lt $SIZE ]; do
    dd if="$FILE" bs="$CHUNK" skip=$((OFFSET/CHUNK)) count=1 status=none | \
      curl -s -X PUT --data-binary @- \
        -H "Authorization: Bearer $KEY" \
        -H "Content-Type: application/octet-stream" \
        "$SERVER/api/uploads/$UPLOAD_ID/chunks?offset=$OFFSET"
    OFFSET=$((OFFSET + CHUNK))
done

# 3. Finalize
curl -s -X POST "$SERVER/api/uploads/$UPLOAD_ID/finalize" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d "{\"sha256\":\"$SHA\"}"
# → {"game_id":"<uuid>"}
```

### Resume

If a PUT fails or the client disconnects:

```bash
STATUS=$(curl -s -H "Authorization: Bearer $KEY" "$SERVER/api/uploads/$UPLOAD_ID")
# STATUS includes received_ranges; compute the gaps and re-PUT those bytes only.
```

### Limits

| Endpoint     | Rate limit (per user) | Body cap |
| ------------ | --------------------- | -------- |
| `init`       | 20/hr                 | 1 MiB    |
| `PUT chunks` | 2000/hr               | 9 MiB    |
| `GET status` | 600/hr                | n/a      |
| `finalize`   | 20/hr                 | 1 MiB    |
| `cancel`     | 60/hr                 | n/a      |

- `sha256` field on finalize is optional; if present, server verifies and rejects on mismatch.
- Upload sessions expire 24 h from creation; expired sessions and partial files are GC'd every 10 minutes.
- Max session size: 500 MiB (same as the existing single-shot limit).
- Below 64 MiB, prefer the existing single-shot `POST /api/games` for fewer round-trips.
````

- [ ] **Step 2: Commit**

```bash
git add docs/DEVELOPER.md
git commit -m "docs: chunked upload — DEVELOPER.md API reference"
```

---

### Task 18: Docs — `SETUP.md` + `README.md` bullet

**Files:**
- Modify: `docs/SETUP.md`
- Modify: `README.md`

- [ ] **Step 1: Soften CF warnings in SETUP.md**

Search for any mention of Cloudflare or body size in `docs/SETUP.md`:

```bash
grep -n -i "cloudflare\|body\|100\s*MB\|upload.*limit" docs/SETUP.md
```

If there's a section warning about CF body caps for game uploads, soften it to:

```markdown
**Reverse proxy body limits:** Game uploads use chunked PUTs (≤ 9 MiB per request) above 64 MiB, so any reverse proxy with a body cap ≥ 9 MiB will work — including Cloudflare Free/Pro (100 MiB). Set `client_max_body_size 16m` or equivalent. Below 64 MiB, the legacy single-shot path is used and the proxy must allow the full file in one request.
```

- [ ] **Step 2: Add a bullet to README.md**

Find the feature/bullet list near the top of `README.md` and add:

```markdown
- Chunked uploads — ship games up to 500 MiB behind Cloudflare Free/Pro or any 9 MiB+ body cap.
```

- [ ] **Step 3: Commit**

```bash
git add docs/SETUP.md README.md
git commit -m "docs: chunked upload — soften CF body-cap warnings + README bullet"
```

---

### Task 19: Run all 18 manual test cases

**Files:**
- Reference: `docs/superpowers/specs/2026-05-21-chunked-upload-design.md` § Test plan

- [ ] **Step 1: Open the spec test table and walk every row**

Open `docs/superpowers/specs/2026-05-21-chunked-upload-design.md`. The "Test plan" section has 18 numbered cases. Execute each one against a fresh build + fresh data dir.

For each case, record PASS / FAIL with notes. Failure path: file an issue inline as a TODO comment + ping the user before continuing.

- [ ] **Step 2: For case 17 (Cloudflare end-to-end), deploy to a CF-fronted instance**

Use the existing playmore deployment workflow. If you don't have a CF-fronted instance ready, skip case 17 here and note that it must be verified post-merge during smoke deployment.

- [ ] **Step 3: Run the Go test suite to confirm range-math tests still pass**

```bash
go test ./internal/models/... -v
```
Expected: 17 tests PASS.

- [ ] **Step 4: Final build sanity**

```bash
go build -o playmore
./playmore --help
```
Expected: clean build, help output prints.

- [ ] **Step 5: Final commit if any fixes were made**

If any test case revealed a bug fixed during this task:

```bash
git add -A
git commit -m "fix: chunked upload — issues found during manual test pass"
```

---

## Self-review checklist

Run through the spec one more time after all 19 tasks are complete:

- [ ] Every endpoint in spec § Protocol is implemented (Task 4-7, 9).
- [ ] DB schema matches spec § Database migration exactly (Task 1).
- [ ] Range coalescing covered by unit tests (Task 2).
- [ ] Per-session mutex prevents race on `received_ranges` (Task 5, 6, 7).
- [ ] Finalize is atomic (`open` → `finalizing` via single UPDATE) (Task 7).
- [ ] sha256 optional, verified when present (Task 7).
- [ ] Cover/screenshots out-of-band on chunked SPA new-game (Task 11).
- [ ] Cover handling out-of-band on chunked CLI new-game (Task 15).
- [ ] 64 MiB threshold in both SPA and CLI (Task 11, 12, 15).
- [ ] CSRF allows octet-stream only on chunk PUT (Task 5).
- [ ] Per-route rate limits match spec table (Task 9).
- [ ] Per-route body caps match spec (Task 9).
- [ ] GC every 10 min, TTL 24h (Task 8).
- [ ] Orphan-file sweep (Task 8).
- [ ] Existing single-shot endpoints unchanged (verified by Task 11 small-file test).
- [ ] DEVELOPER.md updated (Task 17).
- [ ] SETUP.md + README updated (Task 18).
