package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// ============ Follows ============

func FollowDeveloper(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	target, err := models.GetUserByUsername(c.Param("username"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if target.ID == user.ID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot follow yourself"})
		return
	}
	storage.DB.Exec(`INSERT OR IGNORE INTO follows (follower_id, followed_id) VALUES (?, ?)`, user.ID, target.ID)
	CreateNotification(target.ID, "follow", SanitizePlain(user.Username)+" started following you", "", user.Username)
	CheckAchievements(user.ID)
	CheckAchievements(target.ID) // target may unlock "popular"
	c.JSON(http.StatusOK, gin.H{"message": "following " + target.Username})
}

func UnfollowDeveloper(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	target, err := models.GetUserByUsername(c.Param("username"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	storage.DB.Exec(`DELETE FROM follows WHERE follower_id = ? AND followed_id = ?`, user.ID, target.ID)
	c.JSON(http.StatusOK, gin.H{"message": "unfollowed " + target.Username})
}

func GetFollowing(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	rows, err := storage.DB.Query(
		`SELECT u.id, u.username, u.avatar_url, u.bio FROM follows f JOIN users u ON f.followed_id = u.id WHERE f.follower_id = ?`, user.ID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get following"})
		return
	}
	defer rows.Close()

	type FollowedUser struct {
		ID        string `json:"id"`
		Username  string `json:"username"`
		AvatarURL string `json:"avatar_url"`
		Bio       string `json:"bio"`
	}
	users := []FollowedUser{}
	for rows.Next() {
		var u FollowedUser
		rows.Scan(&u.ID, &u.Username, &u.AvatarURL, &u.Bio)
		users = append(users, u)
	}
	c.JSON(http.StatusOK, gin.H{"following": users})
}

func GetFollowerCount(c *gin.Context) {
	username := c.Param("username")
	target, err := models.GetUserByUsername(username)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	var count int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM follows WHERE followed_id = ?`, target.ID).Scan(&count)
	isFollowing := false
	user := middleware.GetUser(c)
	if user != nil {
		var f int
		storage.DB.QueryRow(`SELECT COUNT(*) FROM follows WHERE follower_id = ? AND followed_id = ?`, user.ID, target.ID).Scan(&f)
		isFollowing = f > 0
	}
	c.JSON(http.StatusOK, gin.H{"followers": count, "is_following": isFollowing})
}

// ============ Collections ============

type Collection struct {
	ID          string   `json:"id"`
	UserID      string   `json:"user_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	GameIDs     []string `json:"game_ids"`
	IsPublic    bool     `json:"is_public"`
	Username    string   `json:"username"`
	CreatedAt   string   `json:"created_at"`
}

func scanCollections(rows interface{ Next() bool; Scan(...any) error }) []Collection {
	collections := []Collection{}
	for rows.Next() {
		var col Collection
		var gameIDsJSON string
		rows.Scan(&col.ID, &col.UserID, &col.Name, &col.Description, &gameIDsJSON, &col.IsPublic, &col.Username, &col.CreatedAt)
		json.Unmarshal([]byte(gameIDsJSON), &col.GameIDs)
		if col.GameIDs == nil {
			col.GameIDs = []string{}
		}
		collections = append(collections, col)
	}
	return collections
}

func ListCollections(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	rows, err := storage.DB.Query(`SELECT id, user_id, name, description, game_ids, is_public, username, created_at FROM collections WHERE user_id = ? ORDER BY created_at DESC`, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list collections"})
		return
	}
	defer rows.Close()
	c.JSON(http.StatusOK, gin.H{"collections": scanCollections(rows)})
}

// GetCollection returns a single collection (public or owned)
func GetCollection(c *gin.Context) {
	colID := c.Param("id")
	var col Collection
	var gameIDsJSON string
	err := storage.DB.QueryRow(
		`SELECT id, user_id, name, description, game_ids, is_public, username, created_at FROM collections WHERE id = ?`, colID,
	).Scan(&col.ID, &col.UserID, &col.Name, &col.Description, &gameIDsJSON, &col.IsPublic, &col.Username, &col.CreatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "list not found"})
		return
	}
	json.Unmarshal([]byte(gameIDsJSON), &col.GameIDs)
	if col.GameIDs == nil {
		col.GameIDs = []string{}
	}
	// Check access: public or owner
	user := middleware.GetUser(c)
	if !col.IsPublic && (user == nil || user.ID != col.UserID) {
		c.JSON(http.StatusNotFound, gin.H{"error": "list not found"})
		return
	}
	// Resolve game details
	games := []models.Game{}
	for _, gid := range col.GameIDs {
		g, err := models.GetGameByID(gid)
		if err == nil {
			games = append(games, *g)
		}
	}
	c.JSON(http.StatusOK, gin.H{"collection": col, "games": games})
}

// BrowsePublicLists returns recently created public lists
func BrowsePublicLists(c *gin.Context) {
	rows, err := storage.DB.Query(
		`SELECT c.id, c.user_id, c.name, c.description, c.game_ids, c.is_public, u.username, c.created_at
		 FROM collections c JOIN users u ON c.user_id = u.id
		 WHERE c.is_public = 1 ORDER BY c.created_at DESC LIMIT 20`,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed"})
		return
	}
	defer rows.Close()
	c.JSON(http.StatusOK, gin.H{"collections": scanCollections(rows)})
}

// UpdateCollection updates name, description, visibility
func UpdateCollection(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var input struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		IsPublic    *bool   `json:"is_public"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	colID := c.Param("id")
	if input.Name != nil {
		storage.DB.Exec(`UPDATE collections SET name = ? WHERE id = ? AND user_id = ?`, *input.Name, colID, user.ID)
	}
	if input.Description != nil {
		storage.DB.Exec(`UPDATE collections SET description = ? WHERE id = ? AND user_id = ?`, *input.Description, colID, user.ID)
	}
	if input.IsPublic != nil {
		storage.DB.Exec(`UPDATE collections SET is_public = ? WHERE id = ? AND user_id = ?`, *input.IsPublic, colID, user.ID)
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

type collectionInput struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	IsPublic    bool   `json:"is_public"`
}

func CreateCollection(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var input collectionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := uuid.New().String()
	storage.DB.Exec(`INSERT INTO collections (id, user_id, name, description, is_public, username) VALUES (?, ?, ?, ?, ?, ?)`,
		id, user.ID, input.Name, input.Description, input.IsPublic, user.Username)
	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "collection created"})
}

func DeleteCollection(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	storage.DB.Exec(`DELETE FROM collections WHERE id = ? AND user_id = ?`, c.Param("id"), user.ID)
	c.JSON(http.StatusOK, gin.H{"message": "collection deleted"})
}

type addToCollectionInput struct {
	GameID string `json:"game_id" binding:"required"`
}

func AddToCollection(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var input addToCollectionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	colID := c.Param("id")
	var gameIDsJSON string
	err := storage.DB.QueryRow(`SELECT game_ids FROM collections WHERE id = ? AND user_id = ?`, colID, user.ID).Scan(&gameIDsJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	var gameIDs []string
	json.Unmarshal([]byte(gameIDsJSON), &gameIDs)
	for _, id := range gameIDs {
		if id == input.GameID {
			c.JSON(http.StatusOK, gin.H{"message": "already in collection"})
			return
		}
	}
	gameIDs = append(gameIDs, input.GameID)
	updated, _ := json.Marshal(gameIDs)
	storage.DB.Exec(`UPDATE collections SET game_ids = ? WHERE id = ?`, string(updated), colID)
	c.JSON(http.StatusOK, gin.H{"message": "added to collection"})
}

func RemoveFromCollection(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	colID := c.Param("id")
	gameID := c.Param("game_id")
	var gameIDsJSON string
	err := storage.DB.QueryRow(`SELECT game_ids FROM collections WHERE id = ? AND user_id = ?`, colID, user.ID).Scan(&gameIDsJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	var gameIDs []string
	json.Unmarshal([]byte(gameIDsJSON), &gameIDs)
	filtered := []string{}
	for _, id := range gameIDs {
		if id != gameID {
			filtered = append(filtered, id)
		}
	}
	updated, _ := json.Marshal(filtered)
	storage.DB.Exec(`UPDATE collections SET game_ids = ? WHERE id = ?`, string(updated), colID)
	c.JSON(http.StatusOK, gin.H{"message": "removed from collection"})
}
