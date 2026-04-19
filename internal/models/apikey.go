package models

import (
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

type APIKey struct {
	ID         string  `json:"id"`
	UserID     string  `json:"user_id"`
	Name       string  `json:"name"`
	KeyPrefix  string  `json:"key_prefix"`
	Scopes     string  `json:"scopes"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

func GenerateAPIKey(userID, name, scopes string) (*APIKey, string, error) {
	if scopes == "" {
		scopes = "all"
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	rawKey := "pm_k_" + hex.EncodeToString(b)
	prefix := rawKey[:13]

	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	id := uuid.New().String()
	_, err := storage.DB.Exec(
		`INSERT INTO api_keys (id, user_id, name, key_prefix, key_hash, scopes) VALUES (?, ?, ?, ?, ?, ?)`,
		id, userID, name, prefix, keyHash, scopes,
	)
	if err != nil {
		return nil, "", err
	}

	key := &APIKey{
		ID:        id,
		UserID:    userID,
		Name:      name,
		KeyPrefix: prefix,
		Scopes:    scopes,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return key, rawKey, nil
}

func ValidateAPIKey(rawKey string) (*User, *APIKey, error) {
	if !strings.HasPrefix(rawKey, "pm_k_") || len(rawKey) < 13 {
		return nil, nil, fmt.Errorf("invalid key format")
	}
	prefix := rawKey[:13]

	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	key := &APIKey{}
	var lastUsed *string
	err := storage.DB.QueryRow(
		`SELECT id, user_id, name, key_prefix, scopes, last_used_at, created_at FROM api_keys WHERE key_prefix = ? AND key_hash = ?`,
		prefix, keyHash,
	).Scan(&key.ID, &key.UserID, &key.Name, &key.KeyPrefix, &key.Scopes, &lastUsed, &key.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("invalid API key")
	}
	if err != nil {
		log.Printf("API key validation DB error: %v", err)
		return nil, nil, fmt.Errorf("internal error")
	}
	key.LastUsedAt = lastUsed

	// Capture ID before goroutine to avoid race
	keyID := key.ID
	go func() {
		if _, err := storage.DB.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), keyID); err != nil {
			log.Printf("failed to update API key last_used_at: %v", err)
		}
	}()

	user, err := GetUserByID(key.UserID)
	if err != nil {
		return nil, nil, err
	}
	return user, key, nil
}

func ListAPIKeys(userID string) ([]APIKey, error) {
	rows, err := storage.DB.Query(
		`SELECT id, user_id, name, key_prefix, scopes, last_used_at, created_at FROM api_keys WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var lastUsed *string
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &k.Scopes, &lastUsed, &k.CreatedAt); err != nil {
			log.Printf("failed to scan API key row: %v", err)
			continue
		}
		k.LastUsedAt = lastUsed
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if keys == nil {
		keys = []APIKey{}
	}
	return keys, nil
}

func DeleteAPIKey(id, userID string) error {
	result, err := storage.DB.Exec(`DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, userID)
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

func CountAPIKeys(userID string) (int, error) {
	var count int
	err := storage.DB.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id = ?`, userID).Scan(&count)
	return count, err
}

func HasScope(key *APIKey, scope string) bool {
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
