package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// partialDir returns the directory where in-progress chunked uploads live.
// Path: {dataDir}/uploads/.partial/. dataDir is the parent of GamesDir.
func partialDir() string {
	return filepath.Join(filepath.Dir(GamesDir), "uploads", ".partial")
}

// partialPath returns the on-disk path for upload id `id`.
func partialPath(id string) string {
	return filepath.Join(partialDir(), id+".bin")
}

// CreatePartial creates a sparse file of length `size` for upload `id`.
// Idempotent — if the file already exists with the right size, it's a no-op;
// if it exists with a different size, returns an error.
func CreatePartial(id string, size int64) error {
	if err := os.MkdirAll(partialDir(), 0o750); err != nil {
		return fmt.Errorf("mkdir partial dir: %w", err)
	}
	p := partialPath(id)
	if fi, err := os.Stat(p); err == nil {
		if fi.Size() != size {
			return fmt.Errorf("partial file already exists at unexpected size: got %d want %d", fi.Size(), size)
		}
		return nil
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create partial: %w", err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		os.Remove(p)
		return fmt.Errorf("truncate partial: %w", err)
	}
	return nil
}

// WritePartialAt writes `len(buf)` bytes at `offset` into the partial file for `id`.
// Returns an error if the write would extend past the file's allocated size.
func WritePartialAt(id string, offset int64, buf []byte) error {
	p := partialPath(id)
	f, err := os.OpenFile(p, os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open partial: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat partial: %w", err)
	}
	if offset+int64(len(buf)) > fi.Size() {
		return fmt.Errorf("write past end: offset=%d len=%d size=%d", offset, len(buf), fi.Size())
	}
	if _, err := f.WriteAt(buf, offset); err != nil {
		return fmt.Errorf("write partial: %w", err)
	}
	return nil
}

// OpenPartial opens the partial file read-only. Caller must close.
// Returned *os.File satisfies io.ReaderAt — fits storage.ExtractZipFromReader directly.
func OpenPartial(id string) (*os.File, error) {
	return os.Open(partialPath(id))
}

// DeletePartial removes the partial file. Returns nil if it doesn't exist.
func DeletePartial(id string) error {
	err := os.Remove(partialPath(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListPartialIDs returns the upload IDs of all partial files on disk.
// Used by the GC orphan sweep.
func ListPartialIDs() ([]string, error) {
	entries, err := os.ReadDir(partialDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".bin") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".bin"))
	}
	return ids, nil
}
