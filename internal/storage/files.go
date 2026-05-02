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

// blockedExt are extensions we refuse to extract into game directories.
// Server-side execution risk: even though we serve files as static, a
// misconfigured reverse proxy or future feature could execute these.
var blockedExt = map[string]bool{
	".php": true, ".php3": true, ".php4": true, ".php5": true, ".phtml": true,
	".asp": true, ".aspx": true, ".jsp": true, ".jspx": true, ".cgi": true,
	".pl": true, ".py": true, ".rb": true, ".sh": true, ".bash": true,
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".dll": true,
	".so": true, ".dylib": true, ".bin": true, ".elf": true,
	".htaccess": true, ".htpasswd": true,
}

func isBlockedExtension(name string) bool {
	lower := strings.ToLower(name)
	if blockedExt[lower] {
		return true // .htaccess etc. (no real extension)
	}
	ext := strings.ToLower(filepath.Ext(name))
	return blockedExt[ext]
}

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
// anything containing path separators, traversal segments, or HTML-dangerous
// characters that could XSS via the SPA's onclick interpolation.
// Returns "" if unsafe.
func SanitizeFileName(fileName string) string {
	fileName = strings.ReplaceAll(fileName, "\\", "/")
	fileName = filepath.Base(fileName)
	if fileName == "." || fileName == ".." || fileName == "" || fileName == "/" {
		return ""
	}
	if strings.ContainsAny(fileName, "/\\") || strings.Contains(fileName, "..") {
		return ""
	}
	if !isSafePath(fileName) {
		return ""
	}
	return fileName
}

// ExtractZip extracts a ZIP file to the game directory and returns the entry file name.
// Accepts the ZIP as bytes (legacy callers); prefer ExtractZipFromReader for
// large uploads to avoid pulling the entire file into memory.
func ExtractZip(gameID string, data []byte) (string, error) {
	return ExtractZipFromReader(gameID, bytes.NewReader(data), int64(len(data)))
}

// ExtractZipFromReader extracts from a ReaderAt (typically a temp file) so
// large game uploads don't have to be fully buffered in memory.
func ExtractZipFromReader(gameID string, ra io.ReaderAt, size int64) (string, error) {
	dir := GameDir(gameID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	r, err := zip.NewReader(ra, size)
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
		// Skip server-executable file types — we only host static HTML5 game assets.
		if isBlockedExtension(filepath.Base(name)) {
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

		target := filepath.Clean(filepath.Join(dir, filepath.FromSlash(name)))
		// Prevent path traversal — must be inside dir after Clean (strips ./ and ..)
		if target != dir && !strings.HasPrefix(target, dirWithSep) {
			continue
		}
		// Reject hard links, char devices, etc. — only regular files and dirs.
		// (Already filtered above for symlinks; tighten the same check here.)

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
	// Strict allowlist on entry path to prevent XSS via filename injection
	// into the SPA's onclick handlers. Allow only [a-zA-Z0-9._/-] segments.
	if !isSafePath(entryFile) {
		return "", fmt.Errorf("invalid entry file path — only letters, digits, ., _, -, / allowed")
	}

	return entryFile, nil
}

// isSafePath ensures a relative path contains only safe characters and no
// traversal segments. Used as a defense-in-depth check on game entry files
// (which flow into HTML onclick attributes in the SPA).
func isSafePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return false
	}
	if strings.Contains(p, "..") {
		return false
	}
	for _, r := range p {
		// Allow letters, digits, slash, dot, underscore, hyphen, space.
		// Explicitly reject quotes, angle brackets, ampersand, backtick.
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '/' || r == '.' || r == '_' || r == '-' || r == ' ':
		default:
			return false
		}
	}
	return true
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
