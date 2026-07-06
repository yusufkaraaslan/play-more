package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/webhook"
)

// videoURLAllowedPrefixes — only embed URLs from trusted providers are accepted.
var videoURLAllowedPrefixes = []string{
	"https://www.youtube.com/embed/",
	"https://www.youtube-nocookie.com/embed/",
	"https://player.vimeo.com/video/",
}

func validateVideoURL(url string) bool {
	if url == "" {
		return true // empty is allowed (clears the video)
	}
	for _, prefix := range videoURLAllowedPrefixes {
		if strings.HasPrefix(url, prefix) {
			return true
		}
	}
	return false
}

func ListGames(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "12"))

	params := models.GameListParams{
		Genre:  c.Query("genre"),
		Search: c.Query("search"),
		Sort:   c.DefaultQuery("sort", "newest"),
		Page:   page,
		Limit:  limit,
	}

	games, total, err := models.ListGames(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list games"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"games": games, "total": total, "page": page, "limit": limit})
}

func GetGame(c *gin.Context) {
	id := c.Param("id")
	game, err := models.GetGameByID(id)
	if err != nil {
		game, err = models.GetGameBySlug(id)
	}
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	// Hide unpublished games from anyone but the owner — the list endpoint
	// already filters; this closes the same hole on the by-id getter.
	if !game.Published {
		reqUser := middleware.GetUser(c)
		if reqUser == nil || reqUser.ID != game.DeveloperID {
			c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
			return
		}
	}
	fileSize := storage.GameDirSize(game.ID)
	resp := gin.H{"game": game, "file_size": fileSize}
	if game.Multiplayer {
		// Live players (in a lobby or mid-game) for the multiplayer badge.
		resp["online_players"] = lobby.Default.OnlineCount(game.ID)
	}
	c.JSON(http.StatusOK, resp)
}

func UploadGame(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	title := strings.TrimSpace(c.PostForm("title"))
	genre := c.PostForm("genre")
	description := c.PostForm("description")
	price, _ := strconv.ParseFloat(c.DefaultPostForm("price", "0"), 64)
	if price < 0 {
		price = 0
	}
	tagsStr := c.PostForm("tags")
	isWebGPU := c.PostForm("is_webgpu") == "true"
	multiplayer := c.PostForm("multiplayer") == "true"

	if title == "" || genre == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title and genre are required"})
		return
	}

	tags := []string{}
	if tagsStr != "" {
		for _, t := range strings.Split(tagsStr, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}

	// Create game record
	game, err := models.CreateGame(title, genre, description, user.ID, price, tags, isWebGPU, multiplayer)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create game"})
		return
	}

	// Handle game file
	file, header, err := c.Request.FormFile("game_file")
	if err != nil {
		game.Delete()
		c.JSON(http.StatusBadRequest, gin.H{"error": "game file is required"})
		return
	}
	defer file.Close()

	// Cap upload size at MaxFileSize (500 MiB) to prevent OOM
	if header.Size > storage.MaxFileSize {
		game.Delete()
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large"})
		return
	}

	fileName := storage.SanitizeFileName(header.Filename)
	if fileName == "" {
		game.Delete()
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
		return
	}
	lowerName := strings.ToLower(fileName)
	entryFile := fileName

	// Stream upload to a temp file rather than buffering in memory.
	tmp, err := os.CreateTemp("", "pm-upload-*.bin")
	if err != nil {
		game.Delete()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	written, err := io.Copy(tmp, io.LimitReader(file, storage.MaxFileSize+1))
	if err != nil || written > storage.MaxFileSize {
		game.Delete()
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large"})
		return
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		game.Delete()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	if strings.HasSuffix(lowerName, ".zip") {
		ef, err := storage.ExtractZipFromReader(game.ID, tmp, written)
		if err != nil {
			game.Delete()
			log.Printf("ZIP extraction failed for game %s: %v", game.ID, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid game file"})
			return
		}
		entryFile = ef
	} else if strings.HasSuffix(lowerName, ".html") || strings.HasSuffix(lowerName, ".htm") {
		// Single HTML file is small enough to buffer
		htmlData, err := io.ReadAll(tmp)
		if err != nil {
			game.Delete()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
			return
		}
		if err := storage.SaveGameFile(game.ID, fileName, htmlData); err != nil {
			game.Delete()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
			return
		}
	} else {
		game.Delete()
		c.JSON(http.StatusBadRequest, gin.H{"error": "game file must be .html, .htm, or .zip"})
		return
	}

	game.UpdateFiles(storage.GameDir(game.ID), entryFile)

	// Record the initial upload as build #1 so the game has build
	// history from the start — the builds API and rollback both
	// need a row to point at. The original files live in the game
	// dir (not a builds/ subdir), so the entry stays game-root
	// relative and the retention sweep will never delete this dir
	// (removeBuildDirsUnderGame only touches the builds/ tree).
	if b, err := models.CreateBuild(game.ID, storage.GameDir(game.ID), entryFile, written, "", "", string(models.BuildChannelStable), user.ID); err == nil {
		_ = models.SetActiveBuild(b.ID, game.ID, user.ID)
	} else {
		log.Printf("initial build row for game %s failed: %v", game.ID, err)
	}

	// Handle cover image — must decode as a real image (no HTML-as-PNG XSS).
	coverFile, coverHeader, err := c.Request.FormFile("cover")
	if err == nil {
		defer coverFile.Close()
		declared := strings.ToLower(filepath.Ext(coverHeader.Filename))
		coverData, ext, vErr := ValidateImageBytes(coverFile, declared, maxImageSize)
		if vErr == nil {
			coverName := "cover" + ext
			if sErr := storage.SaveGameFile(game.ID, coverName, coverData); sErr == nil {
				game.UpdateCover("/play/" + game.ID + "/" + coverName)
			}
		}
	}

	// Handle screenshots (multiple) — same validation.
	form, _ := c.MultipartForm()
	if form != nil && form.File["screenshots"] != nil {
		screenshots := []string{}
		for i, fh := range form.File["screenshots"] {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			declared := strings.ToLower(filepath.Ext(fh.Filename))
			data, ext, vErr := ValidateImageBytes(f, declared, maxImageSize)
			f.Close()
			if vErr != nil {
				continue
			}
			name := fmt.Sprintf("screenshot_%d%s", i, ext)
			if err := storage.SaveGameFile(game.ID, name, data); err != nil {
				continue
			}
			screenshots = append(screenshots, "/play/"+game.ID+"/"+name)
		}
		if len(screenshots) > 0 {
			ssJSON, err := json.Marshal(screenshots)
			if err != nil {
				log.Printf("json.Marshal screenshots failed: %v", err)
			} else {
				storage.DB.Exec(`UPDATE games SET screenshots = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(ssJSON), game.ID)
			}
		}

	}

	// Handle video URLs
	videoURL := strings.TrimSpace(c.PostForm("video_url"))
	if videoURL != "" && validateVideoURL(videoURL) {
		videos := []string{videoURL}
		videosJSON, err := json.Marshal(videos)
		if err != nil {
			log.Printf("json.Marshal videos failed: %v", err)
		} else {
			storage.DB.Exec(`UPDATE games SET video_url = ?, videos = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, videoURL, string(videosJSON), game.ID)
		}
	}

	// Mark user as developer
	if !user.IsDeveloper {
		storage.DB.Exec(`UPDATE users SET is_developer = 1 WHERE id = ?`, user.ID)
	}

	models.LogActivity(user.ID, "upload", game.ID, title)
	CheckAchievements(user.ID)

	c.JSON(http.StatusCreated, gin.H{"game": game})
}

func UpdateGame(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	game, err := models.GetGameByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}

	var input struct {
		Title       string   `json:"title" binding:"max=120"`
		Genre       string   `json:"genre" binding:"max=40"`
		Description string   `json:"description" binding:"max=10000"`
		Price       float64  `json:"price"`
		Discount    *int     `json:"discount"`
		Tags        []string `json:"tags"`
		IsWebGPU    bool     `json:"is_webgpu"`
		Multiplayer bool     `json:"multiplayer"`
		Videos      []string `json:"videos"`
		VideoURL    *string  `json:"video_url"`
		ThemeColor  *string  `json:"theme_color"`
		HeaderImage *string  `json:"header_image"`
		CustomAbout *string  `json:"custom_about"`
		Features    []string `json:"features"`
		SysReqMin   *string  `json:"sys_req_min"`
		SysReqRec   *string  `json:"sys_req_rec"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		log.Printf("Validation error in UpdateGame: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input. Please check all fields and try again."})
		return
	}

	// Price must be non-negative — negative prices break checkout math and
	// the storefront UI. Clamp rather than reject so the rest of the update
	// still applies; a client sending a bad value gets the field zeroed.
	if input.Price < 0 {
		input.Price = 0
	}

	if err := game.Update(input.Title, input.Genre, input.Description, input.Price, input.Tags, input.IsWebGPU, input.Multiplayer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update"})
		return
	}

	// Update extended fields — validate video URLs against allowlist
	if input.Videos != nil {
		filtered := []string{}
		for _, v := range input.Videos {
			v = strings.TrimSpace(v)
			if validateVideoURL(v) && v != "" {
				filtered = append(filtered, v)
			}
		}
		videosJSON, err := json.Marshal(filtered)
		if err != nil {
			log.Printf("json.Marshal videos failed: %v", err)
		} else {
			videoURL := ""
			if len(filtered) > 0 {
				videoURL = filtered[0]
			}
			storage.DB.Exec(`UPDATE games SET videos = ?, video_url = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(videosJSON), videoURL, game.ID)
		}
	} else if input.VideoURL != nil && validateVideoURL(*input.VideoURL) {
		storage.DB.Exec(`UPDATE games SET video_url = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, *input.VideoURL, game.ID)
	}
	if input.Discount != nil {
		// Discount is a percent — clamp to [0, 100]. Without this a client
		// could store "999% off" and break price-display math, or "-50%"
		// to inflate the price shown.
		d := *input.Discount
		if d < 0 {
			d = 0
		} else if d > 100 {
			d = 100
		}
		storage.DB.Exec(`UPDATE games SET discount = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, d, game.ID)
	}
	if input.ThemeColor != nil {
		// Validate hex color — flows into style="" attributes on the game page.
		safe := SanitizeColor(*input.ThemeColor)
		storage.DB.Exec(`UPDATE games SET theme_color = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, safe, game.ID)
	}
	if input.HeaderImage != nil {
		// Validate http(s) URL — flows into <img src="">.
		safe := SanitizeWebURL(*input.HeaderImage)
		storage.DB.Exec(`UPDATE games SET header_image = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, safe, game.ID)
	}
	if input.CustomAbout != nil {
		// Stored raw; escaped at render time by the frontend's escapeHtml().
		storage.DB.Exec(`UPDATE games SET custom_about = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, *input.CustomAbout, game.ID)
	}
	if input.Features != nil {
		// Stored raw; each feature is escaped at render time by the frontend's escapeHtml().
		featJSON, err := json.Marshal(input.Features)
		if err != nil {
			log.Printf("json.Marshal features failed: %v", err)
		} else {
			storage.DB.Exec(`UPDATE games SET features = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(featJSON), game.ID)
		}
	}
	if input.SysReqMin != nil {
		// Stored raw; escaped at render time by the frontend's escapeHtml().
		storage.DB.Exec(`UPDATE games SET sys_req_min = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, *input.SysReqMin, game.ID)
	}
	if input.SysReqRec != nil {
		storage.DB.Exec(`UPDATE games SET sys_req_rec = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, *input.SysReqRec, game.ID)
	}

	// Re-fetch updated game
	updated, err := models.GetGameByID(game.ID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"game": game})
		return
	}
	c.JSON(http.StatusOK, gin.H{"game": updated})
}

// ToggleVisibility lets a developer publish/unpublish their own game.
func ToggleVisibility(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	game, err := models.GetGameByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}
	var input struct {
		Published bool `json:"published"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"})
		return
	}
	storage.DB.Exec(`UPDATE games SET published = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, input.Published, game.ID)
	if input.Published {
		webhook.Dispatch(models.WebhookEventGamePublished, user.ID, gin.H{
			"game_id": game.ID,
			"title":   game.Title,
		})
	} else {
		webhook.Dispatch(models.WebhookEventGameUnpublished, user.ID, gin.H{
			"game_id": game.ID,
			"title":   game.Title,
		})
	}
	c.JSON(http.StatusOK, gin.H{"published": input.Published})
}

// UpdateCoverImage replaces a game's cover image. Multipart form field "image"
// is validated as a real image, saved as cover.<ext> in the game directory, and
// game.cover_path is updated to /play/<game.ID>/cover.<ext>. Same storage layout
// as the create-time multipart cover flow, so chunked-upload covers don't end up
// in a separate /uploads/ tree.
func UpdateCoverImage(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	game, err := models.GetGameByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}

	file, header, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image file required"})
		return
	}
	defer file.Close()
	if header.Size > maxImageSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image must be under 5MB"})
		return
	}
	declared := strings.ToLower(filepath.Ext(header.Filename))
	data, ext, vErr := ValidateImageBytes(file, declared, maxImageSize)
	if vErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": vErr.Error()})
		return
	}
	coverName := "cover" + ext
	if err := storage.SaveGameFile(game.ID, coverName, data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save cover"})
		return
	}
	coverPath := "/play/" + game.ID + "/" + coverName
	if err := game.UpdateCover(coverPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update cover"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"cover_path": coverPath})
}

// ManageScreenshots handles adding/removing screenshots for an existing game.
func ManageScreenshots(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	game, err := models.GetGameByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}

	form, _ := c.MultipartForm()
	if form == nil || form.File["screenshots"] == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no screenshots provided"})
		return
	}

	screenshots := append([]string{}, game.Screenshots...)
	baseIdx := len(screenshots)
	for i, fh := range form.File["screenshots"] {
		f, err := fh.Open()
		if err != nil {
			continue
		}
		declared := strings.ToLower(filepath.Ext(fh.Filename))
		data, ext, vErr := ValidateImageBytes(f, declared, maxImageSize)
		f.Close()
		if vErr != nil {
			continue
		}
		name := fmt.Sprintf("screenshot_%d%s", baseIdx+i, ext)
		if err := storage.SaveGameFile(game.ID, name, data); err != nil {
			continue
		}
		screenshots = append(screenshots, "/play/"+game.ID+"/"+name)
	}
	ssJSON, err := json.Marshal(screenshots)
	if err != nil {
		log.Printf("json.Marshal screenshots failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save screenshots"})
		return
	}
	storage.DB.Exec(`UPDATE games SET screenshots = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(ssJSON), game.ID)
	c.JSON(http.StatusOK, gin.H{"screenshots": screenshots})
}

// DeleteScreenshot removes a specific screenshot by index.
func DeleteScreenshot(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	game, err := models.GetGameByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil || idx < 0 || idx >= len(game.Screenshots) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	screenshots := append(game.Screenshots[:idx], game.Screenshots[idx+1:]...)
	ssJSON, err := json.Marshal(screenshots)
	if err != nil {
		log.Printf("json.Marshal screenshots failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update screenshots"})
		return
	}
	storage.DB.Exec(`UPDATE games SET screenshots = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(ssJSON), game.ID)
	c.JSON(http.StatusOK, gin.H{"screenshots": screenshots})
}

func DeleteGame(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	game, err := models.GetGameByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}

	storage.DeleteGameFiles(game.ID)
	game.Delete()

	c.JSON(http.StatusOK, gin.H{"message": "game deleted"})
}

// ReuploadGameFiles replaces game files for an existing game.
//
// This is the single-shot (≤ 500 MiB) reupload path; for larger
// files, use the chunked upload pipeline at
// /api/v1/uploads/{init,chunks,status,finalize}. The reupload
// flow goes through the build-channels machinery: the uploaded
// file is extracted into a new build directory, a build row is
// inserted, and the new build is activated for the `stable`
// channel. The active stable build is what the public sees, so
// promoting a build = publishing. The previous active build
// stays on disk and demoted in the DB; the retention sweep
// (models.CreateBuild) will GC it after MaxBuildsPerGame - 1
// inactive builds accumulate.
//
// The atomicity guarantee from the previous implementation
// (extract-then-swap) is preserved by the build-channels path:
// extraction failure leaves no build row, so games.file_path
// still points at the old build's files. The build row + the
// files in {gameID}/builds/{newBuildID}/ are the new state.
func ReuploadGameFiles(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	game, err := models.GetGameByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
		return
	}
	if game.DeveloperID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your game"})
		return
	}

	file, header, err := c.Request.FormFile("game_file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "game file required"})
		return
	}
	defer file.Close()

	if header.Size > storage.MaxFileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large"})
		return
	}
	fileName := storage.SanitizeFileName(header.Filename)
	if fileName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
		return
	}
	lowerName := strings.ToLower(fileName)
	entryFile := fileName

	// Stream upload to a temp file rather than buffering in memory.
	tmp, err := os.CreateTemp("", "pm-reupload-*.bin")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	written, err := io.Copy(tmp, io.LimitReader(file, storage.MaxFileSize+1))
	if err != nil || written > storage.MaxFileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large"})
		return
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	// Allocate a fresh build ID + directory. The build row is
	// inserted after extraction succeeds; if extraction fails
	// the dir is removed and the row never exists.
	buildID := "build_" + uuid.NewString()
	buildDir := storage.BuildDir(game.ID, buildID)

	switch {
	case strings.HasSuffix(lowerName, ".zip"):
		ef, err := storage.ExtractZipToDir(buildDir, tmp, written)
		if err != nil {
			_ = os.RemoveAll(buildDir)
			log.Printf("ZIP extraction failed for game %s: %v", game.ID, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid game archive"})
			return
		}
		entryFile = ef
	case strings.HasSuffix(lowerName, ".html"), strings.HasSuffix(lowerName, ".htm"):
		if err := os.MkdirAll(buildDir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stage upload"})
			return
		}
		htmlData, err := io.ReadAll(tmp)
		if err != nil {
			_ = os.RemoveAll(buildDir)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
			return
		}
		if err := os.WriteFile(filepath.Join(buildDir, fileName), htmlData, 0644); err != nil {
			_ = os.RemoveAll(buildDir)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "game file must be .html, .htm, or .zip"})
		return
	}

	// The serve handler roots at the game dir, so the stored entry
	// must be relative to it: builds/<buildID>/<entry-within-build>.
	// (ExtractZipToDir / the single-file path both return an entry
	// relative to buildDir.)
	relEntry := filepath.ToSlash(filepath.Join("builds", buildID, entryFile))

	// Register the build row. The retention GC inside
	// CreateBuild keeps MaxBuildsPerGame - 1 older INACTIVE
	// builds; active builds are never deleted.
	build, err := models.CreateBuild(game.ID, buildDir, relEntry, written, "", "", string(models.BuildChannelStable), user.ID)
	if err != nil {
		_ = os.RemoveAll(buildDir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register build"})
		return
	}

	// Promote the new build to active for the stable channel.
	// SetActiveBuild updates games.file_path + games.entry_file
	// inside the same tx, so reads of /api/v1/games/:id after
	// this point see the new files.
	if err := models.SetActiveBuild(build.ID, game.ID, user.ID); err != nil {
		// The build row committed in its own tx; if activation
		// fails, delete it (and its dir) so we don't leave an
		// orphaned row pointing at files the next activate would
		// serve as a dead path.
		_ = models.DeleteBuild(build.ID, game.ID, user.ID)
		_ = os.RemoveAll(buildDir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to activate build"})
		return
	}

	webhook.Dispatch(models.WebhookEventBuildPromoted, user.ID, gin.H{
		"build_id":     build.ID,
		"game_id":      game.ID,
		"build_number": build.BuildNumber,
		"channel":      "stable",
		"via":          "reupload",
	})

	c.JSON(http.StatusOK, gin.H{
		"message":      "game files updated",
		"build_id":     build.ID,
		"build_number": build.BuildNumber,
	})
}

// ServeGameFiles serves game files for the iframe player. spaOrigin is the
// origin allowed to embed via CSP frame-ancestors; pass "" for legacy
// same-origin embed. frame-ancestors also includes the www.<host> variant.
// gamesDomain, when non-empty, is the dedicated host that game content must be
// served from; requests for /play/* arriving on any other host are redirected
// there so uploaded game JS can never execute under the main (authenticated)
// origin's loose game CSP.
func ServeGameFiles(spaOrigin, gamesDomain string) gin.HandlerFunc {
	frameAncestors := "'self'"
	if spaOrigin != "" {
		frameAncestors = spaOrigin
		// If the operator's baseURL is the apex (no www), also allow the www subdomain.
		// If it already starts with https://www., also allow the apex.
		if i := strings.Index(spaOrigin, "://"); i != -1 {
			scheme, host := spaOrigin[:i+3], spaOrigin[i+3:]
			if strings.HasPrefix(host, "www.") {
				frameAncestors += " " + scheme + strings.TrimPrefix(host, "www.")
			} else {
				frameAncestors += " " + scheme + "www." + host
			}
		}
	}
	csp := "default-src * 'unsafe-inline' 'unsafe-eval' data: blob:; img-src * data: blob:; media-src * data: blob:; font-src * data:; connect-src *; frame-ancestors " + frameAncestors
	// htmlCSP applies a browser-enforced sandbox on every HTML game response.
	// Unlike the iframe `sandbox` attribute (which only protects the SPA-launched
	// player), the `sandbox` CSP directive also covers top-level navigation,
	// new-tab opens, and popups — so a direct visit to /play/<id>/index.html
	// cannot mint API keys via same-origin fetch. Omitting allow-same-origin
	// makes the document an opaque origin: cookies aren't sent on /api/* fetches,
	// and any request it does make carries Origin: null (rejected by CSRF).
	// allow-popups-to-escape-sandbox intentionally omitted (#5): a malicious game
	// could otherwise spawn an un-sandboxed popup for credential phishing. Popups
	// stay sandboxed; matches the iframe-attribute sandbox in the SPA player.
	htmlCSP := csp + "; sandbox allow-scripts allow-pointer-lock allow-popups allow-forms allow-modals"

	// gameIDRe matches our game ID format (UUIDv4) — prevents Windows path
	// traversal via gameID = ".." and bounds the lookup to plausible IDs.
	gameIDRe := regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)

	return func(c *gin.Context) {
		// Origin isolation (defends the account-takeover chain): when a dedicated
		// games domain is configured, game HTML must NEVER be served from the
		// main origin. A top-level navigation to playmore.world/play/<id>/ would
		// otherwise run attacker-uploaded JS under the main origin's permissive
		// game CSP, with the victim's first-party session cookie in scope. Redirect
		// any /play/* request on a non-games host to the isolated games origin
		// BEFORE any HTML or loose CSP is emitted.
		if gamesDomain != "" {
			host := c.Request.Host
			if i := strings.IndexByte(host, ':'); i != -1 {
				host = host[:i]
			}
			if !strings.EqualFold(host, gamesDomain) {
				scheme := "https://"
				if !middleware.IsSecure(c) {
					scheme = "http://"
				}
				target := scheme + gamesDomain + c.Request.URL.Path
				if c.Request.URL.RawQuery != "" {
					target += "?" + c.Request.URL.RawQuery
				}
				c.Redirect(http.StatusMovedPermanently, target)
				return
			}
		}

		gameID := c.Param("id")
		if !gameIDRe.MatchString(gameID) {
			c.String(http.StatusNotFound, "game not found")
			return
		}

		// Always look up the game so we can enforce visibility on file serving,
		// not just on the API endpoint. Unpublished games are reachable only by
		// the developer.
		game, err := models.GetGameByID(gameID)
		if err != nil {
			c.String(http.StatusNotFound, "game not found")
			return
		}
		if !game.Published {
			user := middleware.GetUser(c)
			if user == nil || user.ID != game.DeveloperID {
				c.String(http.StatusNotFound, "game not found")
				return
			}
		}

		filePath := c.Param("filepath")
		if filePath == "" || filePath == "/" {
			filePath = "/" + game.EntryFile
		}

		// The builds/ tree holds every build (previous versions, and
		// any non-stable channel). Only the ACTIVE build's own
		// subdirectory may be served publicly — otherwise anyone
		// could fetch a non-active build by guessing its id. The
		// active build's dir is the first two segments of the game's
		// entry_file when it lives under builds/ (builds/<id>/...).
		reqRel := strings.TrimPrefix(filepath.ToSlash(filePath), "/")
		if strings.HasPrefix(reqRel, "builds/") {
			activePrefix := ""
			if strings.HasPrefix(game.EntryFile, "builds/") {
				if parts := strings.SplitN(game.EntryFile, "/", 3); len(parts) >= 2 {
					activePrefix = "builds/" + parts[1] + "/"
				}
			}
			if activePrefix == "" || !strings.HasPrefix(reqRel, activePrefix) {
				c.String(http.StatusNotFound, "not found")
				return
			}
		}

		gameRoot := filepath.Join(storage.GamesDir, gameID)
		fullPath := filepath.Join(gameRoot, filepath.FromSlash(filePath))
		if fullPath != gameRoot && !strings.HasPrefix(fullPath, gameRoot+string(filepath.Separator)) {
			c.String(http.StatusForbidden, "forbidden")
			return
		}

		// CSP: permissive for inert game assets, but any extension that a
		// browser could render as a script-executing document gets a
		// server-enforced sandbox so a direct visit to /play/<id>/<file>
		// cannot mint API keys / read cookies via same-origin fetch.
		// frame-ancestors gates who can embed.
		// Sandboxed types:
		//   .html/.htm  — obvious
		//   .svg        — XML with native <script> support
		//   .xml/.xhtml/.xht — browser-renderable XML docs
		//   .pdf        — PDF.js can execute embedded JS in some contexts
		//   unknown ext — defense-in-depth against MIME sniffing into a doc
		ext := strings.ToLower(filepath.Ext(fullPath))
		needsSandbox := ext == ".html" || ext == ".htm" ||
			ext == ".svg" || ext == ".xml" || ext == ".xhtml" || ext == ".xht" ||
			ext == ".pdf" || contentTypeForExt(ext) == ""
		if needsSandbox {
			c.Header("Content-Security-Policy", htmlCSP)
			// Prevent any popped-out / new-tab game window from reaching back
			// into the SPA via window.opener.
			c.Header("Cross-Origin-Opener-Policy", "same-origin")
		} else {
			c.Header("Content-Security-Policy", csp)
		}
		// XFO can't whitelist a cross-origin host — frame-ancestors is the only way to allow split-origin embed.
		c.Writer.Header().Del("X-Frame-Options")
		// Sandboxed game documents have an opaque origin, so even fetches to
		// their own assets are cross-origin. Allow them. Safe because /play/*
		// only serves public per-game files (auth lives on /api/*).
		c.Header("Access-Control-Allow-Origin", "*")
		if ext == ".wasm" || ext == ".js" || ext == ".css" || ext == ".png" || ext == ".jpg" || ext == ".svg" || ext == ".ogg" || ext == ".mp3" {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		// Force Content-Type from extension allowlist instead of letting
		// http.ServeFile sniff bytes — prevents MIME confusion (e.g. a .png
		// containing HTML being served as text/html).
		if ct := contentTypeForExt(ext); ct != "" {
			c.Header("Content-Type", ct)
		}
		// nosniff stops the browser from second-guessing our Content-Type.
		c.Header("X-Content-Type-Options", "nosniff")
		http.ServeFile(c.Writer, c.Request, fullPath)
	}
}

// contentTypeForExt returns a strict Content-Type for known game asset
// extensions. Returns "" for unknown extensions — http.ServeFile will then
// sniff, but X-Content-Type-Options:nosniff prevents browsers from acting
// on a sniff that disagrees with the extension.
func contentTypeForExt(ext string) string {
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".wasm":
		return "application/wasm"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".ogg":
		return "audio/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".xml":
		return "application/xml; charset=utf-8"
	}
	return ""
}
