package models

import (
	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type Review struct {
	ID        string `json:"id"`
	GameID    string `json:"game_id"`
	UserID    string `json:"user_id"`
	Rating    int    `json:"rating"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
	Username  string `json:"username,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

func CreateReview(gameID, userID string, rating int, text string) (*Review, error) {
	id := uuid.New().String()
	_, err := storage.DB.Exec(
		`INSERT INTO reviews (id, game_id, user_id, rating, text) VALUES (?, ?, ?, ?, ?)`,
		id, gameID, userID, rating, text,
	)
	if err != nil {
		return nil, err
	}
	return &Review{ID: id, GameID: gameID, UserID: userID, Rating: rating, Text: text}, nil
}

func ListReviews(gameID string) ([]Review, error) {
	rows, err := storage.DB.Query(
		`SELECT r.id, r.game_id, r.user_id, r.rating, r.text, r.created_at, u.username, u.avatar_url
		 FROM reviews r JOIN users u ON r.user_id = u.id
		 WHERE r.game_id = ? ORDER BY r.created_at DESC`, gameID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []Review
	for rows.Next() {
		var r Review
		if err := rows.Scan(&r.ID, &r.GameID, &r.UserID, &r.Rating, &r.Text, &r.CreatedAt, &r.Username, &r.AvatarURL); err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	if reviews == nil {
		reviews = []Review{}
	}
	return reviews, nil
}

func DeleteReview(id, userID string) error {
	_, err := storage.DB.Exec(`DELETE FROM reviews WHERE id = ? AND user_id = ?`, id, userID)
	return err
}
