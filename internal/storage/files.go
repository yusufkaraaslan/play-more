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

// Limits for ZIP extraction to prevent decompression bombs.
const (
	MaxExtractedSize  = 2 << 30 // 2 GiB total decompressed size
	MaxExtractedFiles = 10000   // max entries in archive
	MaxFileSize       = 500 << 20 // 500 MiB per file
)

func InitFileStorage(dataDir string) error {
	GamesDir = filepath.Join(dataDir, "games")
	return os.MkdirAll(GamesDir, 0755)
}

func GameDir(gameID string) string {
	return filepath.Join(GamesDir, gameID)
}

// SaveGameFile saves a single file to the game directory.
// fileName is sanitized via filepath.Base + a no-traversal check; callers may
// pass any filename from a multipart upload safely.
func SaveGameFile(gameID string, fileName string, data []byte) error {
	safe := SanitizeFileName(fileName)
	if safe == "" {
		return fmt.Errorf("invalid filename")
	}
	dir := GameDir(gameID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, safe), data, 0644)
}

// SanitizeFileName collapses a multipart filename to its basename and rejects
// anything containing path separators or traversal segments. Returns "" if unsafe.
func SanitizeFileName(fileName string) string {
	fileName = strings.ReplaceAll(fileName, "\\", "/")
	fileName = filepath.Base(fileName)
	if fileName == "." || fileName == ".." || fileName == "" || fileName == "/" {
		return ""
	}
	if strings.ContainsAny(fileName, "/\\") || strings.Contains(fileName, "..") {
		return ""
	}
	return fileName
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

	if len(r.File) > MaxExtractedFiles {
		return "", fmt.Errorf("too many files in archive (max %d)", MaxExtractedFiles)
	}

	// Ensure dir ends with separator for safe traversal check
	dirWithSep := dir
	if !strings.HasSuffix(dirWithSep, string(os.PathSeparator)) {
		dirWithSep += string(os.PathSeparator)
	}

	var totalBytes int64
	entryFile := ""
	for _, f := range r.File {
		name := strings.TrimPrefix(f.Name, prefix)
		if name == "" || strings.HasPrefix(name, "__MACOSX") {
			continue
		}
		// Reject any entry whose name still contains traversal after prefix-strip.
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
			continue
		}
		// Strip dotfiles and dot-directories (.git, .htaccess, .env, etc.).
		skipDot := false
		for _, seg := range strings.Split(filepath.ToSlash(name), "/") {
			if strings.HasPrefix(seg, ".") {
				skipDot = true
				break
			}
		}
		if skipDot {
			continue
		}

		target := filepath.Join(dir, filepath.FromSlash(name))
		// Prevent path traversal — must be inside dir, not just a string prefix match
		if target != dir && !strings.HasPrefix(target, dirWithSep) {
			continue
		}

		// Reject symlinks and other non-regular file modes — only directories and
		// regular files may be extracted. Stops symlink-out / hardlink attacks.
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 || mode&os.ModeDevice != 0 {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0750)
			continue
		}

		// Check declared size before extracting
		if int64(f.UncompressedSize64) > MaxFileSize {
			return "", fmt.Errorf("file %q exceeds max size", name)
		}
		if totalBytes+int64(f.UncompressedSize64) > MaxExtractedSize {
			return "", fmt.Errorf("archive total size exceeds limit")
		}

		if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
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

		// LimitReader as defense against header-lying ZIP bombs
		written, err := io.Copy(out, io.LimitReader(rc, MaxFileSize+1))
		rc.Close()
		out.Close()
		if err != nil {
			return "", err
		}
		if written > MaxFileSize {
			return "", fmt.Errorf("file %q exceeds max size during extraction", name)
		}
		totalBytes += written
		if totalBytes > MaxExtractedSize {
			return "", fmt.Errorf("archive total size exceeds limit during extraction")
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
	// Final defense: refuse entry paths that would escape the game dir at serve time.
	if strings.Contains(entryFile, "..") || strings.HasPrefix(entryFile, "/") {
		return "", fmt.Errorf("invalid entry file path")
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
