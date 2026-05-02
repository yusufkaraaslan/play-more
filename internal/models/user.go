package models

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	Password      string `json:"-"`
	AvatarURL     string `json:"avatar_url"`
	Bio           string `json:"bio"`
	IsDeveloper   bool   `json:"is_developer"`
	BannerURL     string `json:"banner_url"`
	ThemeColor    string `json:"theme_color"`
	Links         []Link `json:"links"`
	AutoplayMedia bool   `json:"autoplay_media"`
	EmailVerified bool   `json:"email_verified"`
	CreatedAt     string `json:"created_at"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// BcryptCost is the work factor for bcrypt. 12 is OWASP 2026 minimum for new
// deployments. Older hashes (cost=10) are silently upgraded on next login via
// CheckPassword → see RehashIfStale.
const BcryptCost = 12

func CreateUser(username, email, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
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

func scanUser(user *User, linksJSON string) {
	json.Unmarshal([]byte(linksJSON), &user.Links)
	if user.Links == nil { user.Links = []Link{} }
}

func GetUserByEmail(email string) (*User, error) {
	user := &User{}
	var linksJSON string
	err := storage.DB.QueryRow(
		`SELECT id, username, email, password, avatar_url, bio, is_developer, banner_url, theme_color, links, autoplay_media, email_verified, created_at FROM users WHERE email = ?`,
		email,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.AvatarURL, &user.Bio, &user.IsDeveloper, &user.BannerURL, &user.ThemeColor, &linksJSON, &user.AutoplayMedia, &user.EmailVerified, &user.CreatedAt)
	if err != nil { return nil, err }
	scanUser(user, linksJSON)
	return user, nil
}

func GetUserByID(id string) (*User, error) {
	user := &User{}
	var linksJSON string
	err := storage.DB.QueryRow(
		`SELECT id, username, email, password, avatar_url, bio, is_developer, banner_url, theme_color, links, autoplay_media, email_verified, created_at FROM users WHERE id = ?`,
		id,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.AvatarURL, &user.Bio, &user.IsDeveloper, &user.BannerURL, &user.ThemeColor, &linksJSON, &user.AutoplayMedia, &user.EmailVerified, &user.CreatedAt)
	if err != nil { return nil, err }
	scanUser(user, linksJSON)
	return user, nil
}

func GetUserByUsername(username string) (*User, error) {
	user := &User{}
	var linksJSON string
	err := storage.DB.QueryRow(
		`SELECT id, username, email, password, avatar_url, bio, is_developer, banner_url, theme_color, links, autoplay_media, email_verified, created_at FROM users WHERE username = ?`,
		username,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.AvatarURL, &user.Bio, &user.IsDeveloper, &user.BannerURL, &user.ThemeColor, &linksJSON, &user.AutoplayMedia, &user.EmailVerified, &user.CreatedAt)
	if err != nil { return nil, err }
	scanUser(user, linksJSON)
	return user, nil
}

func (u *User) CheckPassword(password string) bool {
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)) != nil {
		return false
	}
	// Opportunistic rehash: if the stored hash uses an older cost than the
	// current target, regenerate at BcryptCost. Failure is non-fatal — the
	// existing hash still validates the password.
	if cost, err := bcrypt.Cost([]byte(u.Password)); err == nil && cost < BcryptCost {
		go u.rehashAsync(password)
	}
	return true
}

// rehashAsync is called from CheckPassword on successful login when the stored
// hash is older than BcryptCost. Bcrypt cost-12 takes ~150ms; we don't want to
// block the login response, so it runs in a goroutine.
func (u *User) rehashAsync(password string) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return
	}
	storage.DB.Exec(`UPDATE users SET password = ? WHERE id = ?`, string(hash), u.ID)
}

func (u *User) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return err
	}
	_, err = storage.DB.Exec(`UPDATE users SET password = ? WHERE id = ?`, string(hash), u.ID)
	return err
}

func (u *User) Update(username, bio, avatarURL, bannerURL, themeColor string, links []Link, autoplayMedia bool) error {
	linksJSON, _ := json.Marshal(links)
	_, err := storage.DB.Exec(
		`UPDATE users SET username = ?, bio = ?, avatar_url = ?, banner_url = ?, theme_color = ?, links = ?, autoplay_media = ? WHERE id = ?`,
		username, bio, avatarURL, bannerURL, themeColor, string(linksJSON), autoplayMedia, u.ID,
	)
	return err
}

// Sessions

func CreateSession(userID string) (string, error) {
	// 32 bytes = 256 bits of entropy from crypto/rand
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
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
