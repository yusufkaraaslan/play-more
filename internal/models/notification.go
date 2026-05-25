package models

import (
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// CreateNotification inserts a notification with deduplication within a 5-minute
// window — rapid repeated actions (e.g. follow/unfollow/follow) don't flood the
// recipient's inbox.
func CreateNotification(userID, notifType, message, gameID, fromUser string) {
	var recent int
	storage.DB.QueryRow(
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND type = ? AND from_user = ? AND created_at > datetime('now', '-5 minutes')`,
		userID, notifType, fromUser,
	).Scan(&recent)
	if recent > 0 {
		return
	}
	storage.DB.Exec(
		`INSERT INTO notifications (user_id, type, message, game_id, from_user) VALUES (?, ?, ?, ?, ?)`,
		userID, notifType, message, gameID, fromUser,
	)
}
