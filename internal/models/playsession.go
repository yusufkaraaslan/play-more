package models

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// PlaySession tracks a live game session. The SPA opens one on game
// launch, heartbeats every ~30s while playing, and ends on close.
// admin_analytics counts active sessions (last_heartbeat within 5 min,
// ended_at IS NULL) for the realtime_players metric.
type PlaySession struct {
	SessionID     string  `json:"session_id"`
	UserID        string  `json:"user_id"`
	GameID        string  `json:"game_id"`
	StartedAt     string  `json:"started_at"`
	LastHeartbeat string  `json:"last_heartbeat"`
	EndedAt       *string `json:"ended_at,omitempty"`
}

// OpenPlaySession creates a new play session row. One per (user, game)
// active session — the caller is responsible for ending prior sessions
// if they want one-at-a-time semantics.
func OpenPlaySession(userID, gameID string) (*PlaySession, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := storage.DB.Exec(
		`INSERT INTO play_sessions (session_id, user_id, game_id, started_at, last_heartbeat) VALUES (?, ?, ?, ?, ?)`,
		id, userID, gameID, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("open play session: %w", err)
	}
	return &PlaySession{SessionID: id, UserID: userID, GameID: gameID, StartedAt: now, LastHeartbeat: now}, nil
}

// HeartbeatPlaySession updates last_heartbeat for the session, enforcing
// ownership (user_id must match). Returns an error if the session doesn't
// exist or has already ended.
func HeartbeatPlaySession(sessionID, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := storage.DB.Exec(
		`UPDATE play_sessions SET last_heartbeat = ? WHERE session_id = ? AND user_id = ? AND ended_at IS NULL`,
		now, sessionID, userID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session not found or ended")
	}
	return nil
}

// EndPlaySession marks a play session as ended. Ownership is enforced
// via (session_id, user_id). Idempotent — ending an already-ended
// session is a no-op (returns nil).
func EndPlaySession(sessionID, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := storage.DB.Exec(
		`UPDATE play_sessions SET ended_at = ? WHERE session_id = ? AND user_id = ? AND ended_at IS NULL`,
		now, sessionID, userID,
	)
	if err != nil {
		return err
	}
	// RowsAffected == 0 is fine — session already ended or doesn't exist.
	// Don't error; the caller just wants it ended.
	_ = res
	return nil
}

// CleanupStalePlaySessions deletes play_sessions rows whose
// last_heartbeat is older than 24 hours. Called by the hourly
// cleanup goroutine in main.go.
func CleanupStalePlaySessions() error {
	_, err := storage.DB.Exec(
		`DELETE FROM play_sessions WHERE last_heartbeat < datetime('now', '-24 hours')`,
	)
	return err
}

// CountActivePlaySessions returns the number of sessions with a
// heartbeat within the last 5 minutes and no ended_at. Used by
// admin_analytics for the realtime_players metric.
func CountActivePlaySessions() (int, error) {
	var count int
	err := storage.DB.QueryRow(
		`SELECT COUNT(*) FROM play_sessions WHERE last_heartbeat >= datetime('now', '-5 minutes') AND ended_at IS NULL`,
	).Scan(&count)
	return count, err
}

// CountActivePlaySessionsForGame returns the active session count for
// a specific game. Used by the game detail API for the "online players"
// badge on the game page.
func CountActivePlaySessionsForGame(gameID string) (int, error) {
	var count int
	err := storage.DB.QueryRow(
		`SELECT COUNT(*) FROM play_sessions WHERE game_id = ? AND last_heartbeat >= datetime('now', '-5 minutes') AND ended_at IS NULL`,
		gameID,
	).Scan(&count)
	return count, err
}
