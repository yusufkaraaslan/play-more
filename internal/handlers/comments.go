package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type Comment struct {
	ID        string    `json:"id"`
	DevlogID  string    `json:"devlog_id"`
	UserID    string    `json:"user_id"`
	ParentID  string    `json:"parent_id"`
	Text      string    `json:"text"`
	CreatedAt string    `json:"created_at"`
	Username  string    `json:"username"`
	AvatarURL string    `json:"avatar_url"`
	Replies   []Comment `json:"replies,omitempty"`
}

func ListComments(c *gin.Context) {
	devlogID := c.Param("id")
	rows, err := storage.DB.Query(
		`SELECT c.id, c.devlog_id, c.user_id, c.parent_id, c.text, c.created_at, u.username, u.avatar_url
		 FROM comments c JOIN users u ON c.user_id = u.id
		 WHERE c.devlog_id = ? ORDER BY c.created_at ASC`, devlogID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load comments"})
		return
	}
	defer rows.Close()

	all := []Comment{}
	for rows.Next() {
		var cm Comment
		rows.Scan(&cm.ID, &cm.DevlogID, &cm.UserID, &cm.ParentID, &cm.Text, &cm.CreatedAt, &cm.Username, &cm.AvatarURL)
		all = append(all, cm)
	}

	// Build threaded structure
	byID := map[string]*Comment{}
	roots := []Comment{}
	for i := range all {
		all[i].Replies = []Comment{}
		byID[all[i].ID] = &all[i]
	}
	for i := range all {
		if all[i].ParentID != "" {
			if parent, ok := byID[all[i].ParentID]; ok {
				parent.Replies = append(parent.Replies, all[i])
				continue
			}
		}
		roots = append(roots, all[i])
	}

	// Count
	c.JSON(http.StatusOK, gin.H{"comments": roots, "total": len(all)})
}

type commentInput struct {
	Text     string `json:"text" binding:"required,min=1"`
	ParentID string `json:"parent_id"`
}

func CreateComment(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var input commentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"})
		return
	}

	devlogID := c.Param("id")
	input.Text = SanitizePlain(input.Text)

	// Validate parent_id (if any) belongs to THIS devlog — stops cross-thread
	// notification spam where a user replies to a comment in another thread.
	if input.ParentID != "" {
		var parentDevlog string
		err := storage.DB.QueryRow(`SELECT devlog_id FROM comments WHERE id = ?`, input.ParentID).Scan(&parentDevlog)
		if err != nil || parentDevlog != devlogID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid parent comment"})
			return
		}
	}

	id := uuid.New().String()
	_, err := storage.DB.Exec(
		`INSERT INTO comments (id, devlog_id, user_id, parent_id, text) VALUES (?, ?, ?, ?, ?)`,
		id, devlogID, user.ID, input.ParentID, input.Text,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create comment"})
		return
	}

	// Notify devlog author
	var authorID, devlogTitle string
	storage.DB.QueryRow(`SELECT user_id, title FROM devlogs WHERE id = ?`, devlogID).Scan(&authorID, &devlogTitle)
	if authorID != "" && authorID != user.ID {
		CreateNotification(authorID, "comment", SanitizePlain(user.Username)+" commented on your devlog \""+SanitizePlain(devlogTitle)+"\"", "", user.Username)
	}

	// If replying, notify parent comment author too
	if input.ParentID != "" {
		var parentUserID string
		storage.DB.QueryRow(`SELECT user_id FROM comments WHERE id = ?`, input.ParentID).Scan(&parentUserID)
		if parentUserID != "" && parentUserID != user.ID && parentUserID != authorID {
			CreateNotification(parentUserID, "comment", SanitizePlain(user.Username)+" replied to your comment", "", user.Username)
		}
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "comment posted"})
}

func DeleteComment(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	commentID := c.Param("id")

	// Allow deletion by comment author or devlog author
	var commentUserID, devlogID string
	storage.DB.QueryRow(`SELECT user_id, devlog_id FROM comments WHERE id = ?`, commentID).Scan(&commentUserID, &devlogID)

	var devlogAuthorID string
	storage.DB.QueryRow(`SELECT user_id FROM devlogs WHERE id = ?`, devlogID).Scan(&devlogAuthorID)

	if commentUserID != user.ID && devlogAuthorID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not allowed"})
		return
	}

	// Delete comment and its replies
	storage.DB.Exec(`DELETE FROM comments WHERE id = ? OR parent_id = ?`, commentID, commentID)
	c.JSON(http.StatusOK, gin.H{"message": "comment deleted"})
}
