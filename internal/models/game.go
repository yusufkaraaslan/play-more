package models

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type Game struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	Genre       string   `json:"genre"`
	Price       float64  `json:"price"`
	Discount    int      `json:"discount"`
	Description string   `json:"description"`
	CoverPath   string   `json:"cover_path"`
	DeveloperID string   `json:"developer_id"`
	Tags        []string `json:"tags"`
	IsWebGPU    bool     `json:"is_webgpu"`
	FilePath    string   `json:"file_path"`
	EntryFile   string   `json:"entry_file"`
	Screenshots []string `json:"screenshots"`
	VideoURL    string   `json:"video_url"`
	Published   bool     `json:"published"`
	ThemeColor  string   `json:"theme_color"`
	HeaderImage string   `json:"header_image"`
	CustomAbout string   `json:"custom_about"`
	Features    []string `json:"features"`
	SysReqMin   string   `json:"sys_req_min"`
	SysReqRec   string   `json:"sys_req_rec"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	// Joined fields
	DeveloperName string  `json:"developer_name,omitempty"`
	AvgRating     float64 `json:"avg_rating,omitempty"`
	ReviewCount   int     `json:"review_count,omitempty"`
	PlayCount     int     `json:"play_count,omitempty"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func makeSlug(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func CreateGame(title, genre, description, developerID string, price float64, tags []string, isWebGPU bool) (*Game, error) {
	id := uuid.New().String()
	slug := makeSlug(title)

	// Ensure unique slug
	base := slug
	for i := 1; ; i++ {
		var exists int
		storage.DB.QueryRow(`SELECT COUNT(*) FROM games WHERE slug = ?`, slug).Scan(&exists)
		if exists == 0 {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}

	tagsJSON, _ := json.Marshal(tags)
	screenshotsJSON := "[]"

	game := &Game{
		ID: id, Title: title, Slug: slug, Genre: genre, Price: price,
		Description: description, DeveloperID: developerID, Tags: tags,
		IsWebGPU: isWebGPU, EntryFile: "index.html", Published: true,
	}

	_, err := storage.DB.Exec(
		`INSERT INTO games (id, title, slug, genre, price, description, developer_id, tags, is_webgpu, entry_file, screenshots)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, title, slug, genre, price, description, developerID, string(tagsJSON), isWebGPU, "index.html", screenshotsJSON,
	)
	return game, err
}

func GetGameByID(id string) (*Game, error) {
	return scanGame(storage.DB.QueryRow(
		`SELECT g.id, g.title, g.slug, g.genre, g.price, g.discount, g.description,
		        g.cover_path, g.developer_id, g.tags, g.is_webgpu, g.file_path, g.entry_file,
		        g.screenshots, g.video_url, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE((SELECT AVG(rating) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT COUNT(*) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT SUM(play_count) FROM playtime WHERE game_id = g.id), 0)
		 FROM games g JOIN users u ON g.developer_id = u.id WHERE g.id = ?`, id,
	))
}

func GetGameBySlug(slug string) (*Game, error) {
	return scanGame(storage.DB.QueryRow(
		`SELECT g.id, g.title, g.slug, g.genre, g.price, g.discount, g.description,
		        g.cover_path, g.developer_id, g.tags, g.is_webgpu, g.file_path, g.entry_file,
		        g.screenshots, g.video_url, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE((SELECT AVG(rating) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT COUNT(*) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT SUM(play_count) FROM playtime WHERE game_id = g.id), 0)
		 FROM games g JOIN users u ON g.developer_id = u.id WHERE g.slug = ?`, slug,
	))
}

type GameListParams struct {
	Genre  string
	Search string
	Sort   string
	Page   int
	Limit  int
	DevID  string
}

func ListGames(p GameListParams) ([]Game, int, error) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Limit < 1 || p.Limit > 50 {
		p.Limit = 12
	}

	where := []string{"g.published = 1"}
	args := []any{}

	if p.Genre != "" {
		where = append(where, "g.genre = ?")
		args = append(args, p.Genre)
	}
	if p.Search != "" {
		where = append(where, "(g.title LIKE ? OR g.tags LIKE ?)")
		q := "%" + p.Search + "%"
		args = append(args, q, q)
	}
	if p.DevID != "" {
		where = append(where, "g.developer_id = ?")
		args = append(args, p.DevID)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM games g WHERE `+whereClause, args...).Scan(&total)

	orderBy := "g.created_at DESC"
	switch p.Sort {
	case "popular":
		orderBy = "(SELECT SUM(play_count) FROM playtime WHERE game_id = g.id) DESC"
	case "rating":
		orderBy = "(SELECT AVG(rating) FROM reviews WHERE game_id = g.id) DESC"
	case "price-low":
		orderBy = "g.price ASC"
	case "price-high":
		orderBy = "g.price DESC"
	case "title":
		orderBy = "g.title ASC"
	}

	offset := (p.Page - 1) * p.Limit
	args = append(args, p.Limit, offset)

	rows, err := storage.DB.Query(
		`SELECT g.id, g.title, g.slug, g.genre, g.price, g.discount, g.description,
		        g.cover_path, g.developer_id, g.tags, g.is_webgpu, g.file_path, g.entry_file,
		        g.screenshots, g.video_url, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE((SELECT AVG(rating) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT COUNT(*) FROM reviews WHERE game_id = g.id), 0),
		        COALESCE((SELECT SUM(play_count) FROM playtime WHERE game_id = g.id), 0)
		 FROM games g JOIN users u ON g.developer_id = u.id
		 WHERE `+whereClause+` ORDER BY `+orderBy+` LIMIT ? OFFSET ?`, args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var games []Game
	for rows.Next() {
		g, err := scanGameRow(rows)
		if err != nil {
			return nil, 0, err
		}
		games = append(games, *g)
	}
	return games, total, nil
}

func (g *Game) Update(title, genre, description string, price float64, tags []string, isWebGPU bool) error {
	tagsJSON, _ := json.Marshal(tags)
	_, err := storage.DB.Exec(
		`UPDATE games SET title = ?, genre = ?, description = ?, price = ?, tags = ?, is_webgpu = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		title, genre, description, price, string(tagsJSON), isWebGPU, g.ID,
	)
	return err
}

func (g *Game) UpdateCover(coverPath string) error {
	_, err := storage.DB.Exec(`UPDATE games SET cover_path = ? WHERE id = ?`, coverPath, g.ID)
	return err
}

func (g *Game) UpdateFiles(filePath, entryFile string) error {
	_, err := storage.DB.Exec(`UPDATE games SET file_path = ?, entry_file = ? WHERE id = ?`, filePath, entryFile, g.ID)
	return err
}

func (g *Game) Delete() error {
	_, err := storage.DB.Exec(`DELETE FROM games WHERE id = ?`, g.ID)
	return err
}

// Scanner helpers

type scannable interface {
	Scan(dest ...any) error
}

func parseGameJSON(g *Game, tagsJSON, screenshotsJSON, featuresJSON string) {
	json.Unmarshal([]byte(tagsJSON), &g.Tags)
	json.Unmarshal([]byte(screenshotsJSON), &g.Screenshots)
	json.Unmarshal([]byte(featuresJSON), &g.Features)
	if g.Tags == nil { g.Tags = []string{} }
	if g.Screenshots == nil { g.Screenshots = []string{} }
	if g.Features == nil { g.Features = []string{} }
}

func scanGame(row *sql.Row) (*Game, error) {
	g := &Game{}
	var tagsJSON, screenshotsJSON, featuresJSON string
	err := row.Scan(
		&g.ID, &g.Title, &g.Slug, &g.Genre, &g.Price, &g.Discount, &g.Description,
		&g.CoverPath, &g.DeveloperID, &tagsJSON, &g.IsWebGPU, &g.FilePath, &g.EntryFile,
		&screenshotsJSON, &g.VideoURL, &g.Published,
		&g.ThemeColor, &g.HeaderImage, &g.CustomAbout, &featuresJSON, &g.SysReqMin, &g.SysReqRec,
		&g.CreatedAt, &g.UpdatedAt,
		&g.DeveloperName, &g.AvgRating, &g.ReviewCount, &g.PlayCount,
	)
	if err != nil { return nil, err }
	parseGameJSON(g, tagsJSON, screenshotsJSON, featuresJSON)
	return g, nil
}

func scanGameRow(rows *sql.Rows) (*Game, error) {
	g := &Game{}
	var tagsJSON, screenshotsJSON, featuresJSON string
	err := rows.Scan(
		&g.ID, &g.Title, &g.Slug, &g.Genre, &g.Price, &g.Discount, &g.Description,
		&g.CoverPath, &g.DeveloperID, &tagsJSON, &g.IsWebGPU, &g.FilePath, &g.EntryFile,
		&screenshotsJSON, &g.VideoURL, &g.Published,
		&g.ThemeColor, &g.HeaderImage, &g.CustomAbout, &featuresJSON, &g.SysReqMin, &g.SysReqRec,
		&g.CreatedAt, &g.UpdatedAt,
		&g.DeveloperName, &g.AvgRating, &g.ReviewCount, &g.PlayCount,
	)
	if err != nil { return nil, err }
	parseGameJSON(g, tagsJSON, screenshotsJSON, featuresJSON)
	return g, nil
}
