package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

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
	c.JSON(http.StatusOK, gin.H{"game": game})
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

	data, err := io.ReadAll(file)
	if err != nil {
		game.Delete()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	fileName := header.Filename
	entryFile := fileName

	if strings.HasSuffix(strings.ToLower(fileName), ".zip") {
		ef, err := storage.ExtractZip(game.ID, data)
		if err != nil {
			game.Delete()
			log.Printf("ZIP extraction failed for game %s: %v", game.ID, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid game file"})
			return
		}
		entryFile = ef
	} else {
		if err := storage.SaveGameFile(game.ID, fileName, data); err != nil {
			game.Delete()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
			return
		}
	}

	game.UpdateFiles(storage.GameDir(game.ID), entryFile)

	// Handle cover image
	coverFile, coverHeader, err := c.Request.FormFile("cover")
	if err == nil {
		defer coverFile.Close()
		coverData, _ := io.ReadAll(coverFile)
		coverName := "cover" + filepath.Ext(coverHeader.Filename)
		storage.SaveGameFile(game.ID, coverName, coverData)
		game.UpdateCover("/play/" + game.ID + "/" + coverName)
	}

	// Handle screenshots (multiple)
	form, _ := c.MultipartForm()
	if form != nil && form.File["screenshots"] != nil {
		screenshots := []string{}
		for i, fh := range form.File["screenshots"] {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(f)
			f.Close()
			name := fmt.Sprintf("screenshot_%d%s", i, filepath.Ext(fh.Filename))
			storage.SaveGameFile(game.ID, name, data)
			screenshots = append(screenshots, "/play/"+game.ID+"/"+name)
		}
		if len(screenshots) > 0 {
			ssJSON, _ := json.Marshal(screenshots)
			storage.DB.Exec(`UPDATE games SET screenshots = ? WHERE id = ?`, string(ssJSON), game.ID)
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
		Title       string   `json:"title"`
		Genre       string   `json:"genre"`
		Description string   `json:"description"`
		Price       float64  `json:"price"`
		Tags        []string `json:"tags"`
		IsWebGPU    bool     `json:"is_webgpu"`
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

	c.JSON(http.StatusOK, gin.H{"game": game})
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

// ServeGameFiles serves game files for the iframe player.
func ServeGameFiles(c *gin.Context) {
	gameID := c.Param("id")
	filePath := c.Param("filepath")
	if filePath == "" || filePath == "/" {
		// Look up entry file
		game, err := models.GetGameByID(gameID)
		if err != nil {
			c.String(http.StatusNotFound, "game not found")
			return
		}
		filePath = "/" + game.EntryFile
	}

	fullPath := filepath.Join(storage.GamesDir, gameID, filepath.FromSlash(filePath))
	// Prevent path traversal
	if !strings.HasPrefix(fullPath, storage.GamesDir) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}

	// Games get a permissive CSP — they may load scripts from any CDN
	c.Header("Content-Security-Policy", "default-src * 'unsafe-inline' 'unsafe-eval' data: blob:; img-src * data: blob:; media-src * data: blob:; font-src * data:; connect-src *")
	c.Header("X-Frame-Options", "SAMEORIGIN")
	c.File(fullPath)
}
