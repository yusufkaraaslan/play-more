package models

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// BuildChannel is the named environment a build is published to.
// One build per channel can be active for a given game.
type BuildChannel string

const (
	BuildChannelInternal BuildChannel = "internal"
	BuildChannelBeta     BuildChannel = "beta"
	BuildChannelStable   BuildChannel = "stable"
)

// IsValidBuildChannel reports whether name is one of the
// recognised channel values. Unknown channels are rejected at
// create time so a typo doesn't silently end up at a
// never-active build.
func IsValidBuildChannel(name string) bool {
	switch BuildChannel(name) {
	case BuildChannelInternal, BuildChannelBeta, BuildChannelStable:
		return true
	}
	return false
}

// MaxBuildsPerGame is the retention cap: 5 most recent builds
// per game. Older builds are GC'd when a new one is uploaded.
const MaxBuildsPerGame = 5

// Build is a single uploaded revision of a game, with its
// channel assignment and active flag.
type Build struct {
	ID           string `json:"id"`
	GameID       string `json:"game_id"`
	BuildNumber  int    `json:"build_number"`
	Channel      string `json:"channel"`
	FilePath     string `json:"-"`
	EntryFile    string `json:"entry_file"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	ReleaseNotes string `json:"release_notes"`
	IsActive     bool   `json:"is_active"`
	CreatedAt    string `json:"created_at"`
	CreatedBy    string `json:"created_by"`
}

// CreateBuild inserts a new build row. Returns the build with
// ID + build_number populated. The caller is expected to
// activate it (or leave it inactive) via SetActiveBuild.
//
// We use the existing pattern from the featured / apikey code
// paths: a transaction with a count check to enforce the
// per-game retention cap atomically. After the insert, we run
// a same-transaction GC to delete the oldest (MaxBuildsPerGame
// - 1) builds past the cap.
func CreateBuild(gameID, filePath, entryFile string, size int64, sha256, releaseNotes, channel, createdBy string) (*Build, error) {
	if !IsValidBuildChannel(channel) {
		return nil, errBuildInvalidChannel
	}
	if filePath == "" || entryFile == "" {
		return nil, errBuildMissingPath
	}

	tx, err := storage.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Next build_number is MAX(build_number)+1 for the game. We
	// lock the game's build rows with a SELECT to serialize
	// concurrent uploads (SQLite's default table-level write
	// lock helps, but the SELECT makes the intent explicit).
	var nextNumber int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(build_number), 0) + 1 FROM game_builds WHERE game_id = ?`,
		gameID,
	).Scan(&nextNumber); err != nil {
		return nil, err
	}

	id := "build_" + uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.Exec(
		`INSERT INTO game_builds (id, game_id, build_number, channel, file_path, entry_file, size, sha256, release_notes, is_active, created_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		id, gameID, nextNumber, channel, filePath, entryFile, size, sha256, releaseNotes, now, createdBy,
	)
	if err != nil {
		return nil, err
	}

	// Retention: delete oldest INACTIVE builds past the cap, but
	// never delete an active build. The new build is the most
	// recent, so we keep (MaxBuildsPerGame-1) older ones plus
	// this one. Active builds are excluded — they remain the
	// canonical version of their channel even if history grows
	// past the cap. The cap is therefore a soft limit on
	// inactive history, not a hard cap on total builds.
	_, err = tx.Exec(
		`DELETE FROM game_builds
		 WHERE id IN (
		   SELECT id FROM game_builds
		   WHERE game_id = ? AND id != ? AND is_active = 0
		   ORDER BY build_number DESC
		   LIMIT -1 OFFSET ?
		 )`,
		gameID, id, MaxBuildsPerGame-1,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Build{
		ID:           id,
		GameID:       gameID,
		BuildNumber:  nextNumber,
		Channel:      channel,
		FilePath:     filePath,
		EntryFile:    entryFile,
		Size:         size,
		SHA256:       sha256,
		ReleaseNotes: releaseNotes,
		IsActive:     false,
		CreatedAt:    now,
		CreatedBy:    createdBy,
	}, nil
}

// Sentinel errors.
var (
	errBuildInvalidChannel = errors.New("invalid build channel")
	errBuildMissingPath    = errors.New("build requires file_path and entry_file")
	errBuildNotFound       = errors.New("build not found")
	errBuildNoActive       = errors.New("no active build for this channel")
)

// IsInvalidBuildChannelError reports whether err is the
// unknown-channel sentinel.
func IsInvalidBuildChannelError(err error) bool { return err == errBuildInvalidChannel }

// SetActiveBuild atomically marks one build active and demotes
// the previous active build (if any) for the same game+channel.
// Returns ErrNoRows if the build doesn't exist or isn't owned
// by the given user.
func SetActiveBuild(buildID, gameID, userID string) error {
	tx, err := storage.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Verify ownership (user owns the game).
	var ownerID string
	if err := tx.QueryRow(
		`SELECT developer_id FROM games WHERE id = ?`,
		gameID,
	).Scan(&ownerID); err != nil {
		return err
	}
	if ownerID != userID {
		return sql.ErrNoRows
	}
	// Get the build's channel.
	var channel string
	if err := tx.QueryRow(
		`SELECT channel FROM game_builds WHERE id = ? AND game_id = ?`,
		buildID, gameID,
	).Scan(&channel); err != nil {
		return err
	}
	// Demote current active for that channel.
	if _, err := tx.Exec(
		`UPDATE game_builds SET is_active = 0
		 WHERE game_id = ? AND channel = ? AND is_active = 1`,
		gameID, channel,
	); err != nil {
		return err
	}
	// Promote the new active.
	if _, err := tx.Exec(
		`UPDATE game_builds SET is_active = 1 WHERE id = ?`,
		buildID,
	); err != nil {
		return err
	}
	// Mirror to games.file_path / entry_file for the serve path
	// (which reads games, not game_builds). Only mirror the
	// 'stable' channel — that's what the public sees.
	if channel == string(BuildChannelStable) {
		if _, err := tx.Exec(
			`UPDATE games SET file_path = (SELECT file_path FROM game_builds WHERE id = ?),
			                  entry_file = (SELECT entry_file FROM game_builds WHERE id = ?),
			                  updated_at = CURRENT_TIMESTAMP
			 WHERE id = ?`,
			buildID, buildID, gameID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetBuild returns a build by id. Owner-checked via the parent
// game's developer_id.
func GetBuild(buildID, gameID, userID string) (*Build, error) {
	row := storage.DB.QueryRow(
		`SELECT b.id, b.game_id, b.build_number, b.channel, b.file_path, b.entry_file, b.size, b.sha256, b.release_notes, b.is_active, b.created_at, b.created_by
		 FROM game_builds b
		 JOIN games g ON g.id = b.game_id
		 WHERE b.id = ? AND b.game_id = ? AND g.developer_id = ?`,
		buildID, gameID, userID,
	)
	return scanBuild(row)
}

// ActiveBuild returns the active build for the given game+channel.
// Used by ServeGameFiles (in the future) and by rollback.
func ActiveBuild(gameID, channel string) (*Build, error) {
	row := storage.DB.QueryRow(
		`SELECT id, game_id, build_number, channel, file_path, entry_file, size, sha256, release_notes, is_active, created_at, created_by
		 FROM game_builds
		 WHERE game_id = ? AND channel = ? AND is_active = 1`,
		gameID, channel,
	)
	return scanBuild(row)
}

// PreviousActiveBuild returns the most recent build for the
// channel that isn't the current active one. Used by rollback.
func PreviousActiveBuild(gameID, channel string) (*Build, error) {
	row := storage.DB.QueryRow(
		`SELECT id, game_id, build_number, channel, file_path, entry_file, size, sha256, release_notes, is_active, created_at, created_by
		 FROM game_builds
		 WHERE game_id = ? AND channel = ? AND is_active = 0
		 ORDER BY build_number DESC LIMIT 1`,
		gameID, channel,
	)
	return scanBuild(row)
}

// ListBuilds returns the game's builds, newest first. If
// channel is non-empty, only that channel is returned.
func ListBuilds(gameID, userID, channel string) ([]*Build, error) {
	// Verify ownership.
	var ownerID string
	if err := storage.DB.QueryRow(`SELECT developer_id FROM games WHERE id = ?`, gameID).Scan(&ownerID); err != nil {
		return nil, err
	}
	if ownerID != userID {
		return nil, sql.ErrNoRows
	}

	q := `SELECT id, game_id, build_number, channel, file_path, entry_file, size, sha256, release_notes, is_active, created_at, created_by
	      FROM game_builds WHERE game_id = ?`
	args := []any{gameID}
	if channel != "" {
		q += ` AND channel = ?`
		args = append(args, channel)
	}
	q += ` ORDER BY build_number DESC`
	rows, err := storage.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Build
	for rows.Next() {
		b, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBuild removes a build. Refuses if it's the active
// build for its channel — caller must promote another first.
func DeleteBuild(buildID, gameID, userID string) error {
	// Verify ownership and not-active.
	var ownerID string
	var isActive int
	var channel string
	if err := storage.DB.QueryRow(
		`SELECT g.developer_id, b.is_active, b.channel
		 FROM games g JOIN game_builds b ON b.game_id = g.id
		 WHERE b.id = ? AND b.game_id = ?`,
		buildID, gameID,
	).Scan(&ownerID, &isActive, &channel); err != nil {
		return err
	}
	if ownerID != userID {
		return sql.ErrNoRows
	}
	if isActive == 1 {
		return errors.New("cannot delete the active build for a channel")
	}
	_, err := storage.DB.Exec(`DELETE FROM game_builds WHERE id = ?`, buildID)
	return err
}

// RowScanner is satisfied by both *sql.Row and *sql.Rows.
type RowScanner interface {
	Scan(dest ...any) error
}

func scanBuild(r RowScanner) (*Build, error) {
	b := &Build{}
	if err := r.Scan(
		&b.ID, &b.GameID, &b.BuildNumber, &b.Channel,
		&b.FilePath, &b.EntryFile, &b.Size, &b.SHA256,
		&b.ReleaseNotes, &b.IsActive, &b.CreatedAt, &b.CreatedBy,
	); err != nil {
		return nil, err
	}
	return b, nil
}
