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

// scanIDColumn collects a single-column id result set.
func scanIDColumn(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// featuredPinnedIDs returns published, admin-pinned game IDs in pin order.
func featuredPinnedIDs() ([]string, error) {
	return scanIDColumn(storage.DB.Query(
		`SELECT id FROM games WHERE featured_rank > 0 AND published = 1 ORDER BY featured_rank ASC`,
	))
}

// trendingIDs7d returns published game IDs ranked by view count in the last 7 days.
func trendingIDs7d(limit int) ([]string, error) {
	return scanIDColumn(storage.DB.Query(
		`SELECT g.id FROM games g
		 JOIN game_views v ON v.game_id = g.id
		 WHERE g.published = 1 AND v.created_at >= datetime('now','-7 days')
		 GROUP BY g.id
		 ORDER BY COUNT(*) DESC
		 LIMIT ?`, limit,
	))
}

// newestIDs returns the most recently published game IDs.
func newestIDs(limit int) ([]string, error) {
	return scanIDColumn(storage.DB.Query(
		`SELECT id FROM games WHERE published = 1 ORDER BY created_at DESC LIMIT ?`, limit,
	))
}

// GetFeaturedGames assembles the hero list: pins first, then a trending+newest
// blend, deduped and limited. Returns full Game structs in display order.
func GetFeaturedGames(limit int) ([]Game, error) {
	if limit < 1 || limit > 12 {
		limit = 6
	}
	pinned, err := featuredPinnedIDs()
	if err != nil {
		return nil, err
	}
	trending, err := trendingIDs7d(limit)
	if err != nil {
		return nil, err
	}
	newest, err := newestIDs(limit)
	if err != nil {
		return nil, err
	}
	ids := MergeFeaturedIDs(pinned, trending, newest, limit)
	if len(ids) == 0 {
		return []Game{}, nil
	}
	return GetGamesByIDs(ids) // already reorders to match input id order
}

// GetPinnedGames returns the currently pinned games (for the admin UI), ordered.
func GetPinnedGames() ([]Game, error) {
	ids, err := featuredPinnedIDs()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []Game{}, nil
	}
	return GetGamesByIDs(ids)
}

// SetFeaturedPins replaces the entire pin set: clears all ranks, then assigns
// 1..N in the given id order. Runs in a transaction (DB is SetMaxOpenConns(1)).
func SetFeaturedPins(ids []string) error {
	tx, err := storage.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE games SET featured_rank = 0 WHERE featured_rank > 0`); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE games SET featured_rank = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
