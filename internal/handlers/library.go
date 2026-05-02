package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

func GetLibrary(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	rows, err := storage.DB.Query(
		`SELECT g.id, g.title, g.slug, g.genre, g.price, g.discount, g.description,
		        g.cover_path, g.developer_id, g.tags, g.is_webgpu, g.file_path, g.entry_file,
		        g.screenshots, g.video_url, g.videos, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE((SELECT AVG(rating) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT COUNT(*) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT SUM(play_count) FROM playtime WHERE game_id = g.id), 0)
		 FROM library l
		 JOIN games g ON l.game_id = g.id
		 JOIN users u ON g.developer_id = u.id
		 WHERE l.user_id = ? ORDER BY l.added_at DESC LIMIT 500`, user.ID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load library"})
		return
	}
	defer rows.Close()

	games := []models.Game{}
	for rows.Next() {
		g := models.Game{}
		var tagsJSON, screenshotsJSON, videosJSON, featuresJSON string
		rows.Scan(
			&g.ID, &g.Title, &g.Slug, &g.Genre, &g.Price, &g.Discount, &g.Description,
			&g.CoverPath, &g.DeveloperID, &tagsJSON, &g.IsWebGPU, &g.FilePath, &g.EntryFile,
			&screenshotsJSON, &g.VideoURL, &videosJSON, &g.Published,
			&g.ThemeColor, &g.HeaderImage, &g.CustomAbout, &featuresJSON, &g.SysReqMin, &g.SysReqRec,
			&g.CreatedAt, &g.UpdatedAt,
			&g.DeveloperName, &g.AvgRating, &g.ReviewCount, &g.PlayCount,
		)
		_ = tagsJSON; _ = screenshotsJSON; _ = videosJSON; _ = featuresJSON
		games = append(games, g)
	}
	c.JSON(http.StatusOK, gin.H{"games": games})
}

func AddToLibrary(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("game_id")
	// Reject unpublished games (unless the caller is the developer)
	game, err := models.GetGameByID(gameID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if !game.Published && game.DeveloperID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	_, err = storage.DB.Exec(
		`INSERT OR IGNORE INTO library (user_id, game_id) VALUES (?, ?)`,
		user.ID, gameID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add"})
		return
	}
	models.LogActivity(user.ID, "library_add", gameID, "")
	CheckAchievements(user.ID)
	c.JSON(http.StatusOK, gin.H{"message": "added to library"})
}

func RemoveFromLibrary(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("game_id")
	storage.DB.Exec(`DELETE FROM library WHERE user_id = ? AND game_id = ?`, user.ID, gameID)
	c.JSON(http.StatusOK, gin.H{"message": "removed from library"})
}

func GetWishlist(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	rows, err := storage.DB.Query(
		`SELECT g.id, g.title, g.slug, g.genre, g.price, g.discount, g.description,
		        g.cover_path, g.developer_id, g.tags, g.is_webgpu, g.file_path, g.entry_file,
		        g.screenshots, g.video_url, g.videos, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE((SELECT AVG(rating) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT COUNT(*) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT SUM(play_count) FROM playtime WHERE game_id = g.id), 0)
		 FROM wishlist w
		 JOIN games g ON w.game_id = g.id
		 JOIN users u ON g.developer_id = u.id
		 WHERE w.user_id = ? ORDER BY w.added_at DESC LIMIT 500`, user.ID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load wishlist"})
		return
	}
	defer rows.Close()

	games := []models.Game{}
	for rows.Next() {
		g := models.Game{}
		var tagsJSON, screenshotsJSON, videosJSON, featuresJSON string
		rows.Scan(
			&g.ID, &g.Title, &g.Slug, &g.Genre, &g.Price, &g.Discount, &g.Description,
			&g.CoverPath, &g.DeveloperID, &tagsJSON, &g.IsWebGPU, &g.FilePath, &g.EntryFile,
			&screenshotsJSON, &g.VideoURL, &videosJSON, &g.Published,
			&g.ThemeColor, &g.HeaderImage, &g.CustomAbout, &featuresJSON, &g.SysReqMin, &g.SysReqRec,
			&g.CreatedAt, &g.UpdatedAt,
			&g.DeveloperName, &g.AvgRating, &g.ReviewCount, &g.PlayCount,
		)
		_ = tagsJSON; _ = screenshotsJSON; _ = videosJSON; _ = featuresJSON
		games = append(games, g)
	}
	c.JSON(http.StatusOK, gin.H{"games": games})
}

func AddToWishlist(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("game_id")
	game, err := models.GetGameByID(gameID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if !game.Published && game.DeveloperID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	storage.DB.Exec(`INSERT OR IGNORE INTO wishlist (user_id, game_id) VALUES (?, ?)`, user.ID, gameID)
	c.JSON(http.StatusOK, gin.H{"message": "added to wishlist"})
}

func RemoveFromWishlist(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("game_id")
	storage.DB.Exec(`DELETE FROM wishlist WHERE user_id = ? AND game_id = ?`, user.ID, gameID)
	c.JSON(http.StatusOK, gin.H{"message": "removed from wishlist"})
}
