package models

import (
	"time"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

func CreateEmailToken(token, userID, tokenType string, expires time.Time) error {
	_, err := storage.DB.Exec(
		`INSERT INTO email_tokens (token, user_id, type, expires_at) VALUES (?, ?, ?, ?)`,
		token, userID, tokenType, expires.UTC().Format(time.RFC3339),
	)
	return err
}

func DeleteEmailTokens(userID, tokenType string) {
	storage.DB.Exec(`DELETE FROM email_tokens WHERE user_id = ? AND type = ?`, userID, tokenType)
}

func DeleteEmailTokenByHash(tokenHash string) {
	storage.DB.Exec(`DELETE FROM email_tokens WHERE token = ?`, tokenHash)
}
