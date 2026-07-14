package models

import (
	"database/sql"
	"fmt"
	"regexp"
	"time"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// MaxGameSaveValueBytes caps a single save value at 64 KiB. Saves are
// meant for player state (vehicle designs, progress, settings), not
// asset storage — a game that needs more should split state across
// keys or trim its payload. The route-level body cap in routes.go
// leaves headroom above this so an oversized value gets a clean 413
// from the handler instead of a socket error from MaxBytesReader.
const MaxGameSaveValueBytes = 64 << 10

// MaxGameSaveKeysPerUserGame caps the number of distinct save keys one
// user may hold for one game. Bounds table growth from a buggy save
// loop or a hostile game hammering the endpoint with generated keys.
// 32 keys x 64 KiB = 2 MiB worst case per (user, game).
const MaxGameSaveKeysPerUserGame = 32

// gameSaveKeyRe validates save keys: 1-64 chars of [a-zA-Z0-9._-].
// Keys travel in URL paths, so the alphabet is deliberately tight —
// no separators, no spaces, nothing that needs escaping.
var gameSaveKeyRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// IsValidGameSaveKey reports whether key is an acceptable save key.
func IsValidGameSaveKey(key string) bool {
	return gameSaveKeyRe.MatchString(key)
}

// errGameSaveKeyLimitReached is returned by UpsertGameSave when the
// (user, game) pair already holds MaxGameSaveKeysPerUserGame distinct
// keys and the write would create a new one. Exposed via
// IsGameSaveKeyLimitError.
var errGameSaveKeyLimitReached = fmt.Errorf("game save key limit reached")

// IsGameSaveKeyLimitError reports whether err is the per-(user, game)
// key-cap error, so the PUT handler can surface a 409.
func IsGameSaveKeyLimitError(err error) bool { return err == errGameSaveKeyLimitReached }

// GameSave is one row of the per-user-per-game key-value store. Games
// run in opaque-origin sandboxed iframes where localStorage/IndexedDB
// are unavailable, so this is their only durable storage. Value is a
// raw JSON document, stored verbatim and opaque to the server.
type GameSave struct {
	UserID    string `json:"user_id"`
	GameID    string `json:"game_id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// GameSaveMeta is the value-less listing shape: key, size in bytes,
// and last update time. Used by the list endpoint so a game can
// enumerate its slots without pulling every payload.
type GameSaveMeta struct {
	Key       string `json:"key"`
	Size      int    `json:"size"`
	UpdatedAt string `json:"updated_at"`
}

// UpsertGameSave stores (or replaces) the value under (userID, gameID,
// key) and returns the stored row. The caller is responsible for
// validating the key and the value (IsValidGameSaveKey, json.Valid,
// MaxGameSaveValueBytes) — this function only enforces the key cap.
//
// The existence check, cap check, and write run inside one transaction
// so two concurrent PUTs of different new keys can't both observe
// count < cap and overshoot it (same count-then-insert pattern as
// MintGameSessionToken). Overwriting an existing key never counts
// against the cap.
func UpsertGameSave(userID, gameID, key, value string) (*GameSave, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	err := WithTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM game_saves WHERE user_id = ? AND game_id = ? AND key = ?`,
			userID, gameID, key,
		).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			count, err := CountGameSaveKeys(userID, gameID, tx)
			if err != nil {
				return err
			}
			if count >= MaxGameSaveKeysPerUserGame {
				return errGameSaveKeyLimitReached
			}
		}
		_, err := tx.Exec(
			`INSERT INTO game_saves (user_id, game_id, key, value, updated_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(user_id, game_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			userID, gameID, key, value, now,
		)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &GameSave{UserID: userID, GameID: gameID, Key: key, Value: value, UpdatedAt: now}, nil
}

// GetGameSave fetches one save. Returns sql.ErrNoRows if the key does
// not exist for that (user, game) — the handler maps that to 404.
func GetGameSave(userID, gameID, key string) (*GameSave, error) {
	save := &GameSave{UserID: userID, GameID: gameID, Key: key}
	err := storage.DB.QueryRow(
		`SELECT value, updated_at FROM game_saves WHERE user_id = ? AND game_id = ? AND key = ?`,
		userID, gameID, key,
	).Scan(&save.Value, &save.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return save, nil
}

// ListGameSaves returns key/size/updated_at for every save the user
// holds for the game, ordered by key for deterministic output. Values
// are not returned — a 32-key listing could otherwise weigh 2 MiB.
func ListGameSaves(userID, gameID string) ([]GameSaveMeta, error) {
	rows, err := storage.DB.Query(
		`SELECT key, LENGTH(CAST(value AS BLOB)), updated_at FROM game_saves WHERE user_id = ? AND game_id = ? ORDER BY key`,
		userID, gameID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	saves := []GameSaveMeta{}
	for rows.Next() {
		var m GameSaveMeta
		if err := rows.Scan(&m.Key, &m.Size, &m.UpdatedAt); err != nil {
			return nil, err
		}
		saves = append(saves, m)
	}
	return saves, rows.Err()
}

// DeleteGameSave removes a save. Idempotent — deleting a key that
// doesn't exist is a no-op (returns nil), so a game can retry a
// cleanup call without special-casing "already gone". Ownership is
// enforced in the SQL (user_id AND game_id must match).
func DeleteGameSave(userID, gameID, key string) error {
	_, err := storage.DB.Exec(
		`DELETE FROM game_saves WHERE user_id = ? AND game_id = ? AND key = ?`,
		userID, gameID, key,
	)
	return err
}

// CountGameSaveKeys returns the number of distinct save keys the user
// holds for the game. Pass a tx to read inside UpsertGameSave's
// count-then-insert transaction; pass nil to read the live DB.
func CountGameSaveKeys(userID, gameID string, tx *sql.Tx) (int, error) {
	var count int
	q := `SELECT COUNT(*) FROM game_saves WHERE user_id = ? AND game_id = ?`
	var err error
	if tx != nil {
		err = tx.QueryRow(q, userID, gameID).Scan(&count)
	} else {
		err = storage.DB.QueryRow(q, userID, gameID).Scan(&count)
	}
	return count, err
}
