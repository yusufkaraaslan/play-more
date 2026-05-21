package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
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

	_, err := storage.DB.Exec(`INSERT INTO upload_sessions
		(id, user_id, game_id, kind, filename, size, received_ranges, metadata_json,
		 sha256_expected, status, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, '[]', ?, '', 'open', ?, ?)`,
		s.ID, s.UserID, s.GameID, s.Kind, s.Filename, s.Size,
		s.MetadataJSON, s.CreatedAt, s.ExpiresAt)
	return err
}

// GetUploadSession returns the session by id, or sql.ErrNoRows if missing.
func GetUploadSession(id string) (*UploadSession, error) {
	row := storage.DB.QueryRow(`SELECT id, user_id, game_id, kind, filename, size,
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
	res, err := storage.DB.Exec(`UPDATE upload_sessions SET received_ranges = ? WHERE id = ?`,
		string(buf), id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkFinalizing atomically flips status open→finalizing. Returns true if this
// caller won the race; false means another caller already started finalize.
func MarkFinalizing(id, sha256 string) (bool, error) {
	res, err := storage.DB.Exec(`UPDATE upload_sessions
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
	_, err := storage.DB.Exec(`UPDATE upload_sessions SET status = ? WHERE id = ?`, status, id)
	return err
}

// DeleteUploadSession removes the row by id.
func DeleteUploadSession(id string) error {
	_, err := storage.DB.Exec(`DELETE FROM upload_sessions WHERE id = ?`, id)
	return err
}

// ExpiredOpenSessionIDs returns ids of sessions whose expires_at is in the past
// and whose status is still 'open' — these are GC candidates.
func ExpiredOpenSessionIDs(now time.Time) ([]string, error) {
	rows, err := storage.DB.Query(`SELECT id FROM upload_sessions
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
	rows, err := storage.DB.Query(`SELECT id FROM upload_sessions`)
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
