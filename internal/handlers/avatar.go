package handlers

import (
	"crypto/md5"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

func GetAvatar(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		c.String(http.StatusBadRequest, "username required")
		return
	}

	avatarDir := filepath.Join(storage.GamesDir, "..", "avatars")
	os.MkdirAll(avatarDir, 0755)
	avatarPath := filepath.Join(avatarDir, username+".png")

	if _, err := os.Stat(avatarPath); os.IsNotExist(err) {
		img := generateIdenticon(username, 128)
		f, err := os.Create(avatarPath)
		if err != nil {
			c.String(http.StatusInternalServerError, "failed to generate avatar")
			return
		}
		png.Encode(f, img)
		f.Close()
	}

	c.Header("Cache-Control", "public, max-age=86400")
	c.File(avatarPath)
}

func generateIdenticon(seed string, size int) image.Image {
	hash := md5.Sum([]byte(seed))
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Derive colors from hash
	hue := float64(int(hash[0])<<8 | int(hash[1]))
	h := hue / 65535.0 * 360.0
	fg := hslToRGB(h, 0.65, 0.55)
	bg := color.RGBA{R: 30, G: 30, B: 42, A: 255}

	// Fill background
	for x := 0; x < size; x++ {
		for y := 0; y < size; y++ {
			img.Set(x, y, bg)
		}
	}

	// 5x5 grid, mirrored horizontally (use first 3 columns)
	blockSize := size / 5
	for col := 0; col < 3; col++ {
		for row := 0; row < 5; row++ {
			idx := col*5 + row
			if idx < len(hash) && hash[idx]%2 == 0 {
				drawBlock(img, col*blockSize, row*blockSize, blockSize, fg)
				// Mirror
				drawBlock(img, (4-col)*blockSize, row*blockSize, blockSize, fg)
			}
		}
	}

	return img
}

func drawBlock(img *image.RGBA, x, y, size int, c color.RGBA) {
	for bx := 0; bx < size; bx++ {
		for by := 0; by < size; by++ {
			img.Set(x+bx, y+by, c)
		}
	}
}

func hslToRGB(h, s, l float64) color.RGBA {
	c := (1 - abs(2*l-1)) * s
	x := c * (1 - abs(mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 255,
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func mod(a, b float64) float64 {
	r := a - float64(int(a/b))*b
	if r < 0 {
		r += b
	}
	return r
}

// RegenerateAvatar deletes cached avatar so it gets regenerated on next request.
func RegenerateAvatar(username string) {
	avatarDir := filepath.Join(storage.GamesDir, "..", "avatars")
	os.Remove(filepath.Join(avatarDir, username+".png"))
}

func init() {
	// Ensure fmt is used
	_ = fmt.Sprintf
}
