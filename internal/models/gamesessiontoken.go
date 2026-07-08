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

// GameSessionTokenTTL is the lifetime of a runtime session token.
// Short enough to limit blast radius if leaked, long enough that the
// SPA SDK parent (which refreshes every 4 min) doesn't race the
// expiry. The expires_at is also the hard cut-off — even a still
// un-revoked token stops working at expires_at.
const GameSessionTokenTTL = 5 * time.Minute

// gameSessionTokenPrefixLen is the length of the public prefix stored
// in the DB. pm_gs_ is 6 chars + 8 hex chars = 14. Same convention
// as game_api_keys.
const gameSessionTokenPrefixLen = 14

// MaxActiveGameSessionTokensPerUser caps the number of live (unrevoked,
// unexpired) runtime tokens a single user may hold at once. Bounds
// table growth from a buggy SPA refresh loop or an attacker hammering
// the mint endpoint. A user may legitimately play several games at once
// (each refreshing its own token), so the cap is generous.
const MaxActiveGameSessionTokensPerUser = 20

// errGameSessionTokenLimitReached is returned by MintGameSessionToken
// when the user already holds MaxActiveGameSessionTokensPerUser live
// tokens. Exposed via IsGameSessionTokenLimitError.
var errGameSessionTokenLimitReached = fmt.Errorf("game session token limit reached")

// IsGameSessionTokenLimitError reports whether err is the per-user
// active-token cap error, so the mint handler can surface a 429/400.
func IsGameSessionTokenLimitError(err error) bool { return err == errGameSessionTokenLimitReached }

// GameSessionToken is the row stored for a minted runtime token.
// Phase 0 stores it for two reasons:
//
//  1. Revocation (kick player from room) is a single UPDATE.
//  2. last_used_at is observable for analytics/debugging.
//
// The same unsalted-SHA-256 pattern is used as api_keys and sessions
// — consistent, no new key-derivation surface.
type GameSessionToken struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	GameID     string `json:"game_id"`
	Scopes     string `json:"scopes"`
	ExpiresAt  string `json:"expires_at"`
	Revoked    bool   `json:"revoked"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

// MintGameSessionToken creates a new short-lived runtime token.
// The raw token is returned exactly once; only the hash is stored.
//
// Scopes is a CSV. Use the helper GameSessionTokenDefaultScopes to
// get the Phase 0 default ("session:write"). Future scopes
// (room:join, score:write) are reserved but not used by any
// Phase 0 handler.
func MintGameSessionToken(userID, gameID, scopes string) (*GameSessionToken, string, error) {
	if scopes == "" {
		scopes = GameSessionTokenDefaultScopes
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	rawToken := "pm_gs_" + hex.EncodeToString(b)
	prefix := rawToken[:gameSessionTokenPrefixLen]

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	id := uuid.New().String()
	now := time.Now().UTC()
	expiresAt := now.Add(GameSessionTokenTTL)

	// Count-then-insert inside one transaction so two concurrent mints
	// can't both observe count < cap. WithTx is the only mint path, so
	// the per-user cap cannot be bypassed.
	err := WithTx(func(tx *sql.Tx) error {
		count, err := CountActiveGameSessionTokensForUser(userID, tx)
		if err != nil {
			return err
		}
		if count >= MaxActiveGameSessionTokensPerUser {
			return errGameSessionTokenLimitReached
		}
		_, err = tx.Exec(
			`INSERT INTO game_session_tokens (id, user_id, game_id, token_prefix, token_hash, scopes, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, userID, gameID, prefix, tokenHash, scopes,
			expiresAt.Format(time.RFC3339), now.Format(time.RFC3339),
		)
		return err
	})
	if err != nil {
		return nil, "", err
	}

	tok := &GameSessionToken{
		ID:        id,
		UserID:    userID,
		GameID:    gameID,
		Scopes:    scopes,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		CreatedAt: now.Format(time.RFC3339),
	}
	return tok, rawToken, nil
}

// GameSessionTokenDefaultScopes is the scope string minted for
// Phase 0 tokens. room:*/score:write are reserved for Phase 1; not
// honored by any handler yet.
const GameSessionTokenDefaultScopes = "session:write"

// ValidateGameSessionToken looks up a raw pm_gs_ token. Checks
// token_hash, revoked = 0, and expires_at > now. Updates
// last_used_at asynchronously on success.
//
// Returns (token, user, error). The user is fetched because most
// handlers want to know who's calling.
func ValidateGameSessionToken(rawToken string) (*GameSessionToken, *User, error) {
	if !strings.HasPrefix(rawToken, "pm_gs_") || len(rawToken) < gameSessionTokenPrefixLen {
		return nil, nil, fmt.Errorf("invalid token format")
	}
	prefix := rawToken[:gameSessionTokenPrefixLen]

	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	tok := &GameSessionToken{}
	var lastUsed sql.NullString
	var expiresAt string
	var revoked int
	err := storage.DB.QueryRow(
		`SELECT id, user_id, game_id, scopes, expires_at, revoked, last_used_at, created_at
		 FROM game_session_tokens
		 WHERE token_prefix = ? AND token_hash = ?`,
		prefix, tokenHash,
	).Scan(&tok.ID, &tok.UserID, &tok.GameID, &tok.Scopes, &expiresAt, &revoked, &lastUsed, &tok.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("invalid session token")
	}
	if err != nil {
		log.Printf("game session token validation DB error: %v", err)
		return nil, nil, fmt.Errorf("internal error")
	}
	if revoked == 1 {
		return nil, nil, fmt.Errorf("session token revoked")
	}

	// Check expiry in Go, not in SQL: parse the stored RFC3339
	// string to a time.Time and compare instants. (A raw SQLite
	// string compare against datetime('now') would be wrong — the
	// formats differ; see CountActiveGameSessionTokensForUser.)
	expTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		log.Printf("game session token: bad expires_at format: %q", expiresAt)
		return nil, nil, fmt.Errorf("internal error")
	}
	if time.Now().UTC().After(expTime) {
		return nil, nil, fmt.Errorf("session token expired")
	}
	tok.ExpiresAt = expiresAt
	tok.Revoked = revoked == 1
	if lastUsed.Valid {
		tok.LastUsedAt = lastUsed.String
	}

	// Async last-used update. Capture the DB locally for the
	// same reason as ValidateGameAPIKey: the test harness
	// swaps the package-global storage.DB between tests, so
	// reading it from the goroutine can deref nil.
	tokID := tok.ID
	db := storage.DB
	go func() {
		if _, err := db.Exec(`UPDATE game_session_tokens SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), tokID); err != nil {
			log.Printf("failed to update game session token last_used_at: %v", err)
		}
	}()

	user, err := GetUserByID(tok.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("user not found")
	}
	return tok, user, nil
}

// RevokeGameSessionToken marks a token revoked. The (id, user_id)
// match enforces ownership: a user can only revoke their own tokens.
// Idempotent — re-revoking an already-revoked token still returns nil
// (the row matches the WHERE). Returns "token not found" only when no
// such token exists for that user.
func RevokeGameSessionToken(id, userID string) error {
	// Ownership is enforced in the SQL (id AND user_id). We do NOT
	// gate on `revoked = 0`, so a double-revoke (retry / concurrent
	// call) is idempotent rather than returning a spurious
	// "token not found".
	result, err := storage.DB.Exec(`UPDATE game_session_tokens SET revoked = 1 WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("token not found")
	}
	return nil
}

// CountActiveGameSessionTokensForUser bounds the number of live
// tokens a user can hold. Prevents an attacker (or a buggy SPA
// refresh loop) from accumulating rows forever.
//
// Called from MintGameSessionToken via a transaction; the cap is
// per-user, not per-game, because one user might play many games
// and each game mints its own token.
func CountActiveGameSessionTokensForUser(userID string, tx *sql.Tx) (int, error) {
	var count int
	// expires_at is stored as RFC3339 ("2006-01-02T15:04:05Z"); compare
	// against an RFC3339 "now" bind param, NOT SQLite datetime('now').
	// datetime('now') yields a space-separated, zoneless string that
	// mis-sorts lexicographically against the 'T'/'Z' RFC3339 form
	// (the 'T' at index 10 outranks the space), which would count every
	// same-day token as active regardless of real expiry.
	nowRFC3339 := time.Now().UTC().Format(time.RFC3339)
	q := `SELECT COUNT(*) FROM game_session_tokens WHERE user_id = ? AND revoked = 0 AND expires_at > ?`
	var err error
	if tx != nil {
		err = tx.QueryRow(q, userID, nowRFC3339).Scan(&count)
	} else {
		err = storage.DB.QueryRow(q, userID, nowRFC3339).Scan(&count)
	}
	return count, err
}

// WithTx runs fn inside a transaction on the storage DB. The
// transaction is committed if fn returns nil, rolled back
// otherwise. Used by MintGameSessionToken to count-then-insert
// atomically.
//
// Defined here (not at the call site) so the count-then-insert
// pattern is the only mint path; you can't accidentally insert
// without the cap check.
func WithTx(fn func(tx *sql.Tx) error) error {
	tx, err := storage.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
