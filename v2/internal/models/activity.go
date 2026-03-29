package models

import (
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type Activity struct {
	ID        int    `json:"id"`
	UserID    string `json:"user_id"`
	Type      string `json:"type"`
	GameID    string `json:"game_id"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
	GameTitle string `json:"game_title,omitempty"`
}

func LogActivity(userID, actType, gameID, detail string) error {
	_, err := storage.DB.Exec(
		`INSERT INTO activity (user_id, type, game_id, detail) VALUES (?, ?, ?, ?)`,
		userID, actType, gameID, detail,
	)
	return err
}

func ListActivity(userID string, limit int) ([]Activity, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := storage.DB.Query(
		`SELECT a.id, a.user_id, a.type, COALESCE(a.game_id,''), a.detail, a.created_at,
		        COALESCE(g.title, '')
		 FROM activity a LEFT JOIN games g ON a.game_id = g.id
		 WHERE a.user_id = ? ORDER BY a.created_at DESC LIMIT ?`, userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.UserID, &a.Type, &a.GameID, &a.Detail, &a.CreatedAt, &a.GameTitle); err != nil {
			return nil, err
		}
		activities = append(activities, a)
	}
	if activities == nil {
		activities = []Activity{}
	}
	return activities, nil
}

// RecordPlaytime updates playtime and logs activity.
func RecordPlaytime(userID, gameID string, seconds float64) error {
	_, err := storage.DB.Exec(
		`INSERT INTO playtime (user_id, game_id, total_seconds, last_played, play_count)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP, 1)
		 ON CONFLICT(user_id, game_id) DO UPDATE SET
		     total_seconds = total_seconds + excluded.total_seconds,
		     last_played = CURRENT_TIMESTAMP,
		     play_count = play_count + 1`,
		userID, gameID, seconds,
	)
	return err
}

type UserStats struct {
	GamesOwned   int     `json:"games_owned"`
	HoursPlayed  float64 `json:"hours_played"`
	ReviewCount  int     `json:"review_count"`
	GamesUploaded int    `json:"games_uploaded"`
}

func GetUserStats(userID string) (*UserStats, error) {
	stats := &UserStats{}
	storage.DB.QueryRow(`SELECT COUNT(*) FROM library WHERE user_id = ?`, userID).Scan(&stats.GamesOwned)
	storage.DB.QueryRow(`SELECT COALESCE(SUM(total_seconds)/3600.0, 0) FROM playtime WHERE user_id = ?`, userID).Scan(&stats.HoursPlayed)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM reviews WHERE user_id = ?`, userID).Scan(&stats.ReviewCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM games WHERE developer_id = ?`, userID).Scan(&stats.GamesUploaded)
	return stats, nil
}
