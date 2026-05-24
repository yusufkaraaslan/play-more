package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type FeedItem struct {
	Type      string `json:"type"`      // "devlog", "new_game", "review", "played"
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	GameID    string `json:"game_id"`
	GameTitle string `json:"game_title"`
	CoverPath string `json:"cover_path"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	CreatedAt string `json:"created_at"`
}

func GetFeed(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	items := []FeedItem{}

	// 1. Devlogs from games by followed developers.
	// Filter g.published = 1 — a devlog about an unpublished game shouldn't
	// surface its title/cover to followers before the game itself is public.
	rows1, err := storage.DB.Query(
		`SELECT 'devlog', d.title, d.content, d.game_id, g.title, g.cover_path, u.username, u.avatar_url, d.created_at
		 FROM devlogs d
		 JOIN games g ON d.game_id = g.id
		 JOIN users u ON d.user_id = u.id
		 WHERE d.user_id IN (SELECT followed_id FROM follows WHERE follower_id = ?)
		   AND g.published = 1
		 ORDER BY d.created_at DESC LIMIT 10`, user.ID,
	)
	if err == nil {
		defer rows1.Close()
		for rows1.Next() {
			var item FeedItem
			rows1.Scan(&item.Type, &item.Title, &item.Detail, &item.GameID, &item.GameTitle, &item.CoverPath, &item.Username, &item.AvatarURL, &item.CreatedAt)
			items = append(items, item)
		}
	}

	// 2. New games from followed developers
	rows2, err := storage.DB.Query(
		`SELECT 'new_game', g.title, g.description, g.id, g.title, g.cover_path, u.username, u.avatar_url, g.created_at
		 FROM games g
		 JOIN users u ON g.developer_id = u.id
		 WHERE g.developer_id IN (SELECT followed_id FROM follows WHERE follower_id = ?)
		   AND g.published = 1
		 ORDER BY g.created_at DESC LIMIT 10`, user.ID,
	)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var item FeedItem
			rows2.Scan(&item.Type, &item.Title, &item.Detail, &item.GameID, &item.GameTitle, &item.CoverPath, &item.Username, &item.AvatarURL, &item.CreatedAt)
			items = append(items, item)
		}
	}

	// 3. New reviews on games you own.
	// Filter g.published = 1 — defensive: a review on a game that got
	// unpublished after the fact shouldn't keep surfacing in feeds.
	rows3, err := storage.DB.Query(
		`SELECT 'review', r.text, '', r.game_id, g.title, g.cover_path, u.username, u.avatar_url, r.created_at
		 FROM reviews r
		 JOIN games g ON r.game_id = g.id
		 JOIN users u ON r.user_id = u.id
		 WHERE r.game_id IN (SELECT game_id FROM library WHERE user_id = ?)
		   AND r.user_id != ?
		   AND g.published = 1
		 ORDER BY r.created_at DESC LIMIT 10`, user.ID, user.ID,
	)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var item FeedItem
			rows3.Scan(&item.Type, &item.Title, &item.Detail, &item.GameID, &item.GameTitle, &item.CoverPath, &item.Username, &item.AvatarURL, &item.CreatedAt)
			items = append(items, item)
		}
	}

	// 4. Activity from followed developers (uploads, plays).
	// Filter out activity rows tied to unpublished games — an "uploaded
	// Secret Project X" line right after the upload (before publication)
	// leaks the title to followers. Activity rows with NULL game_id (e.g.
	// follows) pass through via the LEFT JOIN + IS NULL check.
	rows4, err := storage.DB.Query(
		`SELECT a.type, a.detail, '', COALESCE(a.game_id,''), COALESCE(g.title,''), COALESCE(g.cover_path,''), u.username, u.avatar_url, a.created_at
		 FROM activity a
		 JOIN users u ON a.user_id = u.id
		 LEFT JOIN games g ON a.game_id = g.id
		 WHERE a.user_id IN (SELECT followed_id FROM follows WHERE follower_id = ?)
		   AND (g.id IS NULL OR g.published = 1)
		 ORDER BY a.created_at DESC LIMIT 10`, user.ID,
	)
	if err == nil {
		defer rows4.Close()
		for rows4.Next() {
			var item FeedItem
			rows4.Scan(&item.Type, &item.Title, &item.Detail, &item.GameID, &item.GameTitle, &item.CoverPath, &item.Username, &item.AvatarURL, &item.CreatedAt)
			items = append(items, item)
		}
	}

	// Sort by created_at descending (simple string sort works for ISO dates)
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].CreatedAt > items[i].CreatedAt {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	// Limit to 30
	if len(items) > 30 {
		items = items[:30]
	}

	c.JSON(http.StatusOK, gin.H{"feed": items})
}
