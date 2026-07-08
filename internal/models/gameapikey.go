package models

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// GameAPIKey is a long-lived per-game credential. Distinct from APIKey
// (which is user-scoped and can touch user-account routes). Game keys
// are only valid against game-scoped endpoints; the auth branch sets
// auth_method = "game_api_key" so CSRF can distinguish them.
type GameAPIKey struct {
	ID         string  `json:"id"`
	GameID     string  `json:"game_id"`
	Name       string  `json:"name"`
	KeyPrefix  string  `json:"key_prefix"`
	Scopes     string  `json:"scopes"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// MaxGameAPIKeysPerGame caps the number of active game keys per game.
// Kept at 5 because the use case is "CI deploy keys + a couple of
// service accounts", not 10 keys per game like account API keys allow.
const MaxGameAPIKeysPerGame = 5

// errGameKeyLimitReached is returned when the game already has
// MaxGameAPIKeysPerGame keys. Exposed via IsGameKeyLimitError.
var errGameKeyLimitReached = fmt.Errorf("game API key limit reached")

func IsGameKeyLimitError(err error) bool { return err == errGameKeyLimitReached }

// gameAPIKeyPrefixLen is the length of the public prefix stored in
// the DB. pm_gk_ is 6 chars + 8 hex chars = 14. The Validate* path
// takes the first 14 chars of the raw token; both ends must agree.
const gameAPIKeyPrefixLen = 14

// GenerateGameAPIKey mints a new game-scoped key. Returns the
// (public) key row and the raw token (shown once). The token format
// is pm_gk_<64 hex chars> — 32 random bytes = 256 bits of entropy
// from crypto/rand.
//
// name is unique per game (UNIQUE(game_id, name)) — caller picks.
// scopes is a CSV; "all" grants every scope. The full scope
// vocabulary is documented in docs/DEVELOPER.md but only
// session:write gates anything in Phase 0; the rest are reserved
// for Phase 1.
func GenerateGameAPIKey(gameID, name, scopes string) (*GameAPIKey, string, error) {
	if scopes == "" {
		scopes = "all"
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	rawKey := "pm_gk_" + hex.EncodeToString(b)
	prefix := rawKey[:gameAPIKeyPrefixLen]

	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	id := uuid.New().String()

	// Same atomic-count pattern as GenerateAPIKey: BEGIN IMMEDIATE
	// so two concurrent inserts can't both observe count < cap.
	tx, err := storage.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, "", err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM game_api_keys WHERE game_id = ?`, gameID).Scan(&count); err != nil {
		tx.Rollback()
		return nil, "", err
	}
	if count >= MaxGameAPIKeysPerGame {
		tx.Rollback()
		return nil, "", errGameKeyLimitReached
	}
	if _, err := tx.Exec(
		`INSERT INTO game_api_keys (id, game_id, name, key_prefix, key_hash, scopes) VALUES (?, ?, ?, ?, ?, ?)`,
		id, gameID, name, prefix, keyHash, scopes,
	); err != nil {
		tx.Rollback()
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}

	key := &GameAPIKey{
		ID:        id,
		GameID:    gameID,
		Name:      name,
		KeyPrefix: prefix,
		Scopes:    scopes,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return key, rawKey, nil
}

// ValidateGameAPIKey looks up a raw pm_gk_ token. Returns the row
// (and a placeholder "user" — game keys are game-scoped, not
// user-scoped, so the user field is the game's developer to keep
// the (User, *APIKey) signature symmetric with the account-key path).
//
// The developer is the game's developer_id. Handlers that need the
// developer can call GetUserByID(key.UserID); handlers that only
// need game_id use key.GameID.
func ValidateGameAPIKey(rawKey string) (*GameAPIKey, error) {
	if !strings.HasPrefix(rawKey, "pm_gk_") || len(rawKey) < gameAPIKeyPrefixLen {
		return nil, fmt.Errorf("invalid key format")
	}
	prefix := rawKey[:gameAPIKeyPrefixLen]

	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	key := &GameAPIKey{}
	var lastUsed *string
	err := storage.DB.QueryRow(
		`SELECT id, game_id, name, key_prefix, scopes, last_used_at, created_at FROM game_api_keys WHERE key_prefix = ? AND key_hash = ?`,
		prefix, keyHash,
	).Scan(&key.ID, &key.GameID, &key.Name, &key.KeyPrefix, &key.Scopes, &lastUsed, &key.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid API key")
	}
	if err != nil {
		log.Printf("game API key validation DB error: %v", err)
		return nil, fmt.Errorf("internal error")
	}
	key.LastUsedAt = lastUsed

	// Async last-used update — same pattern as account keys.
	// Capture the DB locally so the goroutine doesn't read
	// storage.DB at runtime — in tests the harness swaps the
	// global DB back to (potentially nil) between tests, which
	// would panic the goroutine after the test that spawned it
	// has already returned.
	keyID := key.ID
	db := storage.DB
	go func() {
		if _, err := db.Exec(`UPDATE game_api_keys SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), keyID); err != nil {
			log.Printf("failed to update game API key last_used_at: %v", err)
		}
	}()

	return key, nil
}

// ListGameAPIKeys returns the masked keys for a game.
func ListGameAPIKeys(gameID string) ([]GameAPIKey, error) {
	rows, err := storage.DB.Query(
		`SELECT id, game_id, name, key_prefix, scopes, last_used_at, created_at FROM game_api_keys WHERE game_id = ? ORDER BY created_at DESC`,
		gameID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []GameAPIKey
	for rows.Next() {
		var k GameAPIKey
		var lastUsed *string
		if err := rows.Scan(&k.ID, &k.GameID, &k.Name, &k.KeyPrefix, &k.Scopes, &lastUsed, &k.CreatedAt); err != nil {
			log.Printf("failed to scan game API key row: %v", err)
			continue
		}
		k.LastUsedAt = lastUsed
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if keys == nil {
		keys = []GameAPIKey{}
	}
	return keys, nil
}

// DeleteGameAPIKey removes a key. Owner-check is the caller's
// responsibility (handler queries the game, verifies developer_id).
func DeleteGameAPIKey(id, gameID string) error {
	result, err := storage.DB.Exec(`DELETE FROM game_api_keys WHERE id = ? AND game_id = ?`, id, gameID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("key not found")
	}
	return nil
}

// GameAPIKeyHasScope returns true if the key grants the given scope.
// "all" grants everything. Used by the RequireScope middleware.
func GameAPIKeyHasScope(key *GameAPIKey, scope string) bool {
	if key.Scopes == "all" {
		return true
	}
	for _, s := range strings.Split(key.Scopes, ",") {
		if strings.TrimSpace(s) == scope {
			return true
		}
	}
	return false
}
