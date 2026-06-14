package models

import (
	"database/sql"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// MergeFeaturedIDs builds the ordered featured list: pinned IDs first (in the
// given order), then auto-fill slots alternating trending/newest, skipping any
// ID already used, until `limit` is reached or both sources are exhausted.
func MergeFeaturedIDs(pinned, trending, newest []string, limit int) []string {
	if limit < 0 {
		limit = 0
	}
	result := make([]string, 0, limit)
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || seen[id] || len(result) >= limit {
			return
		}
		seen[id] = true
		result = append(result, id)
	}
	for _, id := range pinned {
		add(id)
	}
	ti, ni := 0, 0
	useTrending := true
	for len(result) < limit && (ti < len(trending) || ni < len(newest)) {
		if useTrending && ti < len(trending) {
			add(trending[ti])
			ti++
		} else if !useTrending && ni < len(newest) {
			add(newest[ni])
			ni++
		} else if ti < len(trending) {
			add(trending[ti])
			ti++
		} else if ni < len(newest) {
			add(newest[ni])
			ni++
		}
		useTrending = !useTrending
	}
	return result
}

// --- DB-backed ID sources (implemented in Task 3) live in this file too. ---
var _ = sql.ErrNoRows // placeholder; removed when Task 3 adds real usage
var _ = storage.DB    // placeholder; removed when Task 3 adds real usage
