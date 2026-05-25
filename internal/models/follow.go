package models

import (
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

func FollowUser(followerID, followedID string) {
	storage.DB.Exec(`INSERT OR IGNORE INTO follows (follower_id, followed_id) VALUES (?, ?)`, followerID, followedID)
}

func UnfollowUser(followerID, followedID string) {
	storage.DB.Exec(`DELETE FROM follows WHERE follower_id = ? AND followed_id = ?`, followerID, followedID)
}
