package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Password    string `json:"-"`
	AvatarURL   string `json:"avatar_url"`
	Bio         string `json:"bio"`
	IsDeveloper bool   `json:"is_developer"`
	CreatedAt   string `json:"created_at"`
}

func CreateUser(username, email, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &User{
		ID:       uuid.New().String(),
		Username: username,
		Email:    email,
		Password: string(hash),
	}

	_, err = storage.DB.Exec(
		`INSERT INTO users (id, username, email, password) VALUES (?, ?, ?, ?)`,
		user.ID, user.Username, user.Email, user.Password,
	)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func GetUserByEmail(email string) (*User, error) {
	user := &User{}
	err := storage.DB.QueryRow(
		`SELECT id, username, email, password, avatar_url, bio, is_developer, created_at FROM users WHERE email = ?`,
		email,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.AvatarURL, &user.Bio, &user.IsDeveloper, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func GetUserByID(id string) (*User, error) {
	user := &User{}
	err := storage.DB.QueryRow(
		`SELECT id, username, email, password, avatar_url, bio, is_developer, created_at FROM users WHERE id = ?`,
		id,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.AvatarURL, &user.Bio, &user.IsDeveloper, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func GetUserByUsername(username string) (*User, error) {
	user := &User{}
	err := storage.DB.QueryRow(
		`SELECT id, username, email, password, avatar_url, bio, is_developer, created_at FROM users WHERE username = ?`,
		username,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.AvatarURL, &user.Bio, &user.IsDeveloper, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (u *User) CheckPassword(password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)) == nil
}

func (u *User) Update(username, bio, avatarURL string) error {
	_, err := storage.DB.Exec(
		`UPDATE users SET username = ?, bio = ?, avatar_url = ? WHERE id = ?`,
		username, bio, avatarURL, u.ID,
	)
	return err
}

// Sessions

func CreateSession(userID string) (string, error) {
	token := uuid.New().String()
	expires := time.Now().Add(30 * 24 * time.Hour) // 30 days
	_, err := storage.DB.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expires,
	)
	return token, err
}

func GetUserBySession(token string) (*User, error) {
	var userID string
	err := storage.DB.QueryRow(
		`SELECT user_id FROM sessions WHERE token = ? AND expires_at > datetime('now')`,
		token,
	).Scan(&userID)
	if err != nil {
		return nil, err
	}
	return GetUserByID(userID)
}

func DeleteSession(token string) error {
	_, err := storage.DB.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}
