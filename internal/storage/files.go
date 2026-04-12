package storage

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var GamesDir string

func InitFileStorage(dataDir string) error {
	GamesDir = filepath.Join(dataDir, "games")
	return os.MkdirAll(GamesDir, 0755)
}

func GameDir(gameID string) string {
	return filepath.Join(GamesDir, gameID)
}

// SaveGameFile saves a single HTML/JS file to the game directory.
func SaveGameFile(gameID string, fileName string, data []byte) error {
	dir := GameDir(gameID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fileName), data, 0644)
}

// ExtractZip extracts a ZIP file to the game directory and returns the entry file name.
func ExtractZip(gameID string, data []byte) (string, error) {
	dir := GameDir(gameID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}

	// Find common prefix (if all files share a root directory)
	prefix := ""
	if len(r.File) > 0 {
		first := r.File[0].Name
		if idx := strings.IndexByte(first, '/'); idx > 0 {
			candidate := first[:idx+1]
			allMatch := true
			for _, f := range r.File {
				if !strings.HasPrefix(f.Name, candidate) {
					allMatch = false
					break
				}
			}
			if allMatch {
				prefix = candidate
			}
		}
	}

	entryFile := ""
	for _, f := range r.File {
		name := strings.TrimPrefix(f.Name, prefix)
		if name == "" || strings.HasPrefix(name, "__MACOSX") {
			continue
		}

		target := filepath.Join(dir, filepath.FromSlash(name))
		// Prevent path traversal
		if !strings.HasPrefix(target, dir) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return "", err
		}

		rc, err := f.Open()
		if err != nil {
			return "", err
		}

		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return "", err
		}

		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return "", err
		}

		// Detect entry file
		if entryFile == "" && strings.EqualFold(filepath.Base(name), "index.html") {
			entryFile = name
		}
	}

	if entryFile == "" {
		// Fallback: find any .html file
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".html") {
				rel, _ := filepath.Rel(dir, path)
				entryFile = rel
				return filepath.SkipAll
			}
			return nil
		})
	}

	if entryFile == "" {
		return "", fmt.Errorf("no HTML file found in ZIP")
	}

	return entryFile, nil
}

// GameDirSize returns total size of all files in a game directory in bytes.
func GameDirSize(gameID string) int64 {
	var size int64
	filepath.Walk(GameDir(gameID), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// DeleteGameFiles removes all files for a game.
func DeleteGameFiles(gameID string) error {
	return os.RemoveAll(GameDir(gameID))
}
