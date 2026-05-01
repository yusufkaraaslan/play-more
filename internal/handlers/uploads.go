package handlers

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
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

// PNG, JPG, GIF have stdlib decoders. WebP would require golang.org/x/image —
// dropped to keep dep surface minimal; users can convert before upload.
var imageAllowedExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
}

// ValidateImageBytes reads up to maxBytes, decodes as an image to confirm
// it's actually a real image (not HTML disguised by extension), and returns
// the bytes plus the canonical extension matching the decoded format.
// Returns error if the bytes don't decode as a known image format.
func ValidateImageBytes(r io.Reader, declaredExt string, maxBytes int64) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("image too large")
	}
	_, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("not a valid image")
	}
	// Canonicalize extension from decoded format so HTML-with-.png-name can't be served as text/html.
	ext := "." + strings.ToLower(format)
	if ext == ".jpeg" {
		ext = ".jpg"
	}
	// The declared extension must match the decoded format (allows .jpg/.jpeg interchange).
	declared := strings.ToLower(declaredExt)
	if declared == ".jpeg" {
		declared = ".jpg"
	}
	if !imageAllowedExt[declared] || (declared != ext && !(declared == ".jpg" && ext == ".jpg")) {
		// Allow declared ext to differ from decoded only if both are in our allowlist
		// (some browsers send .png for .webp); trust the decoded format.
		if !imageAllowedExt[ext] {
			return nil, "", fmt.Errorf("unsupported image format")
		}
	}
	return data, ext, nil
}

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

	declaredExt := strings.ToLower(filepath.Ext(header.Filename))
	if !imageAllowedExt[declaredExt] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported image format"})
		return
	}

	data, ext, err := ValidateImageBytes(file, declaredExt, maxImageSize)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	uploadDir := filepath.Join(storage.GamesDir, "..", "uploads", user.ID)
	os.MkdirAll(uploadDir, 0750)

	filename := uuid.New().String() + ext
	fullPath := filepath.Join(uploadDir, filename)
	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}

	url := "/uploads/" + user.ID + "/" + filename
	c.JSON(http.StatusOK, gin.H{"url": url})
}
