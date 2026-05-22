// Package uploadgc runs a background sweep that cleans up expired upload
// sessions and orphan partial files. It lives in its own package to avoid
// the import cycle between internal/models (which imports internal/storage
// for the DB handle) and the GC code (which needs both models and storage).
package uploadgc

import (
	"context"
	"log"
	"time"

	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// Interval is how often the sweep runs.
const Interval = 10 * time.Minute

// Start launches a background goroutine that periodically deletes expired
// upload_sessions (status='open' AND expires_at < now) and orphan partial
// files (files with no matching session row).
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
}

func sweep() {
	// 1. Expire stale sessions
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

	// 2. Orphan file sweep — files with no session row
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
