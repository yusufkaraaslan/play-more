package uploadgc

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

const Interval = 10 * time.Minute

// UploadsGCEnabled controls whether the daily uploads/ directory sweep runs.
// Disabled by default — when enabled it deletes unreferenced files older than
// 90 days, which is destructive enough that operators should opt in after
// reviewing what the dry-run logs would prune. Set via main.go flag plumbing.
var UploadsGCEnabled bool

// UploadsGCDryRun, when true alongside UploadsGCEnabled, logs the would-be
// deletions without actually removing anything. Useful for operators to
// validate the reference check before flipping the switch for real.
var UploadsGCDryRun bool

func Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sweep()
			}
		}
	}()

	if UploadsGCEnabled {
		go func() {
			if UploadsGCDryRun {
				log.Printf("uploadgc: uploads-sweep enabled in DRY-RUN mode (no files will be deleted)")
			} else {
				log.Printf("uploadgc: uploads-sweep enabled (90-day retention, real deletes)")
			}
			t := time.NewTicker(24 * time.Hour)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					sweepUploads()
				}
			}
		}()
	}
}

func sweep() {
	ids, err := models.ExpiredOpenSessionIDs(time.Now().UTC())
	if err != nil {
		log.Printf("uploadgc: expired query: %v", err)
	}
	for _, id := range ids {
		if err := storage.DeletePartial(id); err != nil {
			log.Printf("uploadgc: delete partial %s: %v", id, err)
		}
		if err := models.DeleteUploadSession(id); err != nil {
			log.Printf("uploadgc: delete session %s: %v", id, err)
		}
	}

	knownIDs, err := models.AllSessionIDs()
	if err != nil {
		log.Printf("uploadgc: list sessions: %v", err)
		return
	}
	fileIDs, err := storage.ListPartialIDs()
	if err != nil {
		log.Printf("uploadgc: list partials: %v", err)
		return
	}
	for _, id := range fileIDs {
		if _, ok := knownIDs[id]; ok {
			continue
		}
		if err := storage.DeletePartial(id); err != nil {
			log.Printf("uploadgc: delete orphan partial %s: %v", id, err)
		}
	}
}

var uploadsDir string

func uploadDir() string {
	if uploadsDir != "" {
		return uploadsDir
	}
	uploadsDir = filepath.Join(storage.GamesDir, "..", "uploads")
	return uploadsDir
}

func sweepUploads() {
	dir := uploadDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -90)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.ModTime().Before(cutoff) {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}

		if isUploadReferenced(rel) {
			return nil
		}

		ageDays := int(time.Since(info.ModTime()).Hours() / 24)
		if UploadsGCDryRun {
			log.Printf("uploadgc: DRY-RUN would prune %s (%d days old, unreferenced)", rel, ageDays)
			return nil
		}
		if err := os.Remove(path); err != nil {
			log.Printf("uploadgc: remove %s: %v", rel, err)
		} else {
			log.Printf("uploadgc: pruned unreferenced upload %s (%d days old)", rel, ageDays)
		}
		return nil
	})
	if err != nil {
		log.Printf("uploadgc: walk uploads: %v", err)
	}

	removeEmptyUploadDirs(dir)
}

func removeEmptyUploadDirs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		subEntries, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		if len(subEntries) == 0 {
			if err := os.Remove(sub); err == nil {
				log.Printf("uploadgc: removed empty upload dir %s", e.Name())
			}
		}
	}
}

func isUploadReferenced(rel string) bool {
	escaped := "%" + strings.ReplaceAll(rel, "_", "\\_") + "%"

	var found int
	err := storage.DB.QueryRow(`
		SELECT 1 FROM games WHERE
			cover_path = ? OR header_image = ? OR
			screenshots LIKE ? ESCAPE '\' OR videos LIKE ? ESCAPE '\' OR
			features LIKE ? ESCAPE '\'
		UNION ALL
		SELECT 1 FROM users WHERE
			avatar_url = ? OR banner_url = ?
		UNION ALL
		SELECT 1 FROM developer_pages WHERE banner_url = ?
		LIMIT 1
	`, rel, rel, escaped, escaped, escaped,
		rel, rel,
		rel).Scan(&found)

	if err != nil && err != sql.ErrNoRows {
		log.Printf("uploadgc: reference check for %s: %v", rel, err)
		return true
	}
	return found == 1
}
