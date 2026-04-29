package handlers

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

const maxImageSize = 5 * 1024 * 1024 // 5MB

func UploadImage(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
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

	// Validate extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}
	if !allowed[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported image format"})
		return
	}

	// Create upload directory
	uploadDir := filepath.Join(storage.GamesDir, "..", "uploads", user.ID)
	os.MkdirAll(uploadDir, 0755)

	// Generate unique filename
	filename := uuid.New().String() + ext
	fullPath := filepath.Join(uploadDir, filename)

	// Stream to disk with hard cap (defense against header.Size being incorrect)
	out, err := os.Create(fullPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}
	written, err := io.Copy(out, io.LimitReader(file, maxImageSize+1))
	out.Close()
	if err != nil || written > maxImageSize {
		os.Remove(fullPath)
		c.JSON(http.StatusBadRequest, gin.H{"error": "image too large"})
		return
	}

	url := "/uploads/" + user.ID + "/" + filename
	c.JSON(http.StatusOK, gin.H{"url": url})
}
