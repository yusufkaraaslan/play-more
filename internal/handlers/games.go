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
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
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
	c.JSON(http.StatusOK, gin.H{"game": game, "file_size": fileSize})
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
	tagsStr := c.PostForm("tags")
	isWebGPU := c.PostForm("is_webgpu") == "true"

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
	game, err := models.CreateGame(title, genre, description, user.ID, price, tags, isWebGPU)
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
		htmlData, _ := io.ReadAll(tmp)
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
			ssJSON, _ := json.Marshal(screenshots)
			storage.DB.Exec(`UPDATE games SET screenshots = ? WHERE id = ?`, string(ssJSON), game.ID)
		}
	}

	// Handle video URLs
	videoURL := strings.TrimSpace(c.PostForm("video_url"))
	if videoURL != "" && validateVideoURL(videoURL) {
		videos := []string{videoURL}
		videosJSON, _ := json.Marshal(videos)
		storage.DB.Exec(`UPDATE games SET video_url = ?, videos = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, videoURL, string(videosJSON), game.ID)
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
		Title       string   `json:"title"`
		Genre       string   `json:"genre"`
		Description string   `json:"description"`
		Price       float64  `json:"price"`
		Discount    *int     `json:"discount"`
		Tags        []string `json:"tags"`
		IsWebGPU    bool     `json:"is_webgpu"`
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

	if err := game.Update(input.Title, input.Genre, input.Description, input.Price, input.Tags, input.IsWebGPU); err != nil {
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
		videosJSON, _ := json.Marshal(filtered)
		videoURL := ""
		if len(filtered) > 0 {
			videoURL = filtered[0]
		}
		storage.DB.Exec(`UPDATE games SET videos = ?, video_url = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(videosJSON), videoURL, game.ID)
	} else if input.VideoURL != nil && validateVideoURL(*input.VideoURL) {
		storage.DB.Exec(`UPDATE games SET video_url = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, *input.VideoURL, game.ID)
	}
	if input.Discount != nil {
		storage.DB.Exec(`UPDATE games SET discount = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, *input.Discount, game.ID)
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
		storage.DB.Exec(`UPDATE games SET custom_about = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, *input.CustomAbout, game.ID)
	}
	if input.Features != nil {
		featJSON, _ := json.Marshal(input.Features)
		storage.DB.Exec(`UPDATE games SET features = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(featJSON), game.ID)
	}
	if input.SysReqMin != nil {
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
	c.JSON(http.StatusOK, gin.H{"published": input.Published})
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
	ssJSON, _ := json.Marshal(screenshots)
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
	ssJSON, _ := json.Marshal(screenshots)
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

	// Delete old files
	storage.DeleteGameFiles(game.ID)

	if strings.HasSuffix(lowerName, ".zip") {
		ef, err := storage.ExtractZipFromReader(game.ID, tmp, written)
		if err != nil {
			log.Printf("ZIP extraction failed for game %s: %v", game.ID, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid game archive"})
			return
		}
		entryFile = ef
	} else if strings.HasSuffix(lowerName, ".html") || strings.HasSuffix(lowerName, ".htm") {
		htmlData, _ := io.ReadAll(tmp)
		if err := storage.SaveGameFile(game.ID, fileName, htmlData); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
			return
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "game file must be .html, .htm, or .zip"})
		return
	}
	game.UpdateFiles(storage.GameDir(game.ID), entryFile)
	c.JSON(http.StatusOK, gin.H{"message": "game files updated"})
}

// ServeGameFiles serves game files for the iframe player. spaOrigin is the
// origin allowed to embed via CSP frame-ancestors; pass "" for legacy
// same-origin embed. frame-ancestors also includes the www.<host> variant.
func ServeGameFiles(spaOrigin string) gin.HandlerFunc {
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

	// gameIDRe matches our game ID format (UUIDv4) — prevents Windows path
	// traversal via gameID = ".." and bounds the lookup to plausible IDs.
	gameIDRe := regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)

	return func(c *gin.Context) {
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

		gameRoot := filepath.Join(storage.GamesDir, gameID)
		fullPath := filepath.Join(gameRoot, filepath.FromSlash(filePath))
		if fullPath != gameRoot && !strings.HasPrefix(fullPath, gameRoot+string(filepath.Separator)) {
			c.String(http.StatusForbidden, "forbidden")
			return
		}

		// Permissive CSP for game assets; frame-ancestors gates who can embed.
		// Iframe sandbox is the primary defense against malicious game code.
		c.Header("Content-Security-Policy", csp)
		// XFO can't whitelist a cross-origin host — frame-ancestors is the only way to allow split-origin embed.
		c.Writer.Header().Del("X-Frame-Options")
		ext := strings.ToLower(filepath.Ext(fullPath))
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
