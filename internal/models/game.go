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
	Videos      []string `json:"videos"`
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
	baseSlug := makeSlug(title)
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, fmt.Errorf("json marshal tags: %w", err)
	}
	screenshotsJSON := "[]"

	// Try inserting with slug, retry with suffix on UNIQUE conflict
	var slug string
	for attempt := 0; attempt < 20; attempt++ {
		if attempt == 0 {
			slug = baseSlug
		} else {
			slug = fmt.Sprintf("%s-%d", baseSlug, attempt)
		}
		_, err = storage.DB.Exec(
			`INSERT INTO games (id, title, slug, genre, price, description, developer_id, tags, is_webgpu, entry_file, screenshots)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, title, slug, genre, price, description, developerID, string(tagsJSON), isWebGPU, "index.html", screenshotsJSON,
		)
		if err == nil {
			break
		}
		if !storage.IsUniqueConstraintError(err) {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}

	game := &Game{
		ID: id, Title: title, Slug: slug, Genre: genre, Price: price,
		Description: description, DeveloperID: developerID, Tags: tags,
		IsWebGPU: isWebGPU, EntryFile: "index.html", Published: true,
	}
	return game, nil
}

func GetGameByID(id string) (*Game, error) {
	return scanGame(storage.DB.QueryRow(
		`SELECT g.id, g.title, g.slug, g.genre, g.price, g.discount, g.description,
		        g.cover_path, g.developer_id, g.tags, g.is_webgpu, g.file_path, g.entry_file,
		        g.screenshots, g.video_url, g.videos, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE(ride.avg_rating, 0),
		        COALESCE(ride.review_count, 0),
		        COALESCE(pc.play_count, 0)
		 FROM games g
		 JOIN users u ON g.developer_id = u.id
		 LEFT JOIN (SELECT game_id, AVG(rating) AS avg_rating, COUNT(*) AS review_count FROM reviews GROUP BY game_id) ride ON ride.game_id = g.id
		 LEFT JOIN (SELECT game_id, SUM(play_count) AS play_count FROM playtime GROUP BY game_id) pc ON pc.game_id = g.id
		 WHERE g.id = ?`, id,
	))
}

func GetGameBySlug(slug string) (*Game, error) {
	return scanGame(storage.DB.QueryRow(
		`SELECT g.id, g.title, g.slug, g.genre, g.price, g.discount, g.description,
		        g.cover_path, g.developer_id, g.tags, g.is_webgpu, g.file_path, g.entry_file,
		        g.screenshots, g.video_url, g.videos, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE(ride.avg_rating, 0),
		        COALESCE(ride.review_count, 0),
		        COALESCE(pc.play_count, 0)
		 FROM games g
		 JOIN users u ON g.developer_id = u.id
		 LEFT JOIN (SELECT game_id, AVG(rating) AS avg_rating, COUNT(*) AS review_count FROM reviews GROUP BY game_id) ride ON ride.game_id = g.id
		 LEFT JOIN (SELECT game_id, SUM(play_count) AS play_count FROM playtime GROUP BY game_id) pc ON pc.game_id = g.id
		 WHERE g.slug = ?`, slug,
	))
}

type GameListParams struct {
	Genre      string
	Search     string
	Sort       string
	Page       int
	Limit      int
	DevID      string
	IncludeAll bool // include unpublished (for developer's own dashboard)
}

func ListGames(p GameListParams) ([]Game, int, error) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Limit < 1 || p.Limit > 50 {
		p.Limit = 12
	}

	where := []string{}
	args := []any{}
	if !p.IncludeAll {
		where = append(where, "g.published = 1")
	}

	if p.Genre != "" {
		where = append(where, "g.genre = ?")
		args = append(args, p.Genre)
	}
	if p.Search != "" {
		// Cap search length to prevent FTS5 query DoS via huge inputs
		if len(p.Search) > 100 {
			p.Search = p.Search[:100]
		}
		where = append(where, "(g.rowid IN (SELECT rowid FROM games_fts WHERE games_fts MATCH ?) OR g.title LIKE ? OR g.tags LIKE ?)")
		// Escape FTS special characters (incl. AND/OR/NOT keywords via space-stripping) to prevent query injection
		safe := strings.Map(func(r rune) rune {
			if strings.ContainsRune(`+-<>():*"^~`, r) {
				return -1
			}
			return r
		}, p.Search)
		ftsQuery := safe + "*"
		q := "%" + p.Search + "%"
		args = append(args, ftsQuery, q, q)
	}
	if p.DevID != "" {
		where = append(where, "g.developer_id = ?")
		args = append(args, p.DevID)
	}

	whereClause := "1=1"
	if len(where) > 0 {
		whereClause = strings.Join(where, " AND ")
	}

	var total int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM games g WHERE `+whereClause, args...).Scan(&total)

	orderBy := "g.created_at DESC"
	switch p.Sort {
	case "popular":
		orderBy = "COALESCE(pc.play_count, 0) DESC"
	case "rating":
		orderBy = "COALESCE(ride.avg_rating, 0) DESC"
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
		        g.screenshots, g.video_url, g.videos, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE(ride.avg_rating, 0),
		        COALESCE(ride.review_count, 0),
		        COALESCE(pc.play_count, 0)
		 FROM games g
		 JOIN users u ON g.developer_id = u.id
		 LEFT JOIN (SELECT game_id, AVG(rating) AS avg_rating, COUNT(*) AS review_count FROM reviews GROUP BY game_id) ride ON ride.game_id = g.id
		 LEFT JOIN (SELECT game_id, SUM(play_count) AS play_count FROM playtime GROUP BY game_id) pc ON pc.game_id = g.id
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
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("json marshal tags: %w", err)
	}
	_, err = storage.DB.Exec(
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

func GetGamesByIDs(ids []string) ([]Game, error) {
	if len(ids) == 0 {
		return []Game{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		`SELECT g.id, g.title, g.slug, g.genre, g.price, g.discount, g.description,
		        g.cover_path, g.developer_id, g.tags, g.is_webgpu, g.file_path, g.entry_file,
		        g.screenshots, g.video_url, g.videos, g.published, g.theme_color, g.header_image, g.custom_about, g.features, g.sys_req_min, g.sys_req_rec, g.created_at, g.updated_at,
		        u.username,
		        COALESCE(ride.avg_rating, 0),
		        COALESCE(ride.review_count, 0),
		        COALESCE(pc.play_count, 0)
		 FROM games g
		 JOIN users u ON g.developer_id = u.id
		 LEFT JOIN (SELECT game_id, AVG(rating) AS avg_rating, COUNT(*) AS review_count FROM reviews GROUP BY game_id) ride ON ride.game_id = g.id
		 LEFT JOIN (SELECT game_id, SUM(play_count) AS play_count FROM playtime GROUP BY game_id) pc ON pc.game_id = g.id
		 WHERE g.id IN (%s)`, strings.Join(placeholders, ","),
	)
	rows, err := storage.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// SQL IN(...) returns rows in storage order, not in the input id order —
	// so a collection's user-curated ordering would be lost. Build a map and
	// reorder to match the caller's input ids.
	byID := make(map[string]Game, len(ids))
	for rows.Next() {
		g, err := scanGameRow(rows)
		if err != nil {
			return nil, err
		}
		byID[g.ID] = *g
	}
	ordered := make([]Game, 0, len(ids))
	for _, id := range ids {
		if g, ok := byID[id]; ok {
			ordered = append(ordered, g)
		}
	}
	return ordered, nil
}

func (g *Game) Delete() error {
	_, err := storage.DB.Exec(`DELETE FROM games WHERE id = ?`, g.ID)
	return err
}

// Scanner helpers

type scannable interface {
	Scan(dest ...any) error
}

func parseGameJSON(g *Game, tagsJSON, screenshotsJSON, videosJSON, featuresJSON string) {
	json.Unmarshal([]byte(tagsJSON), &g.Tags)
	json.Unmarshal([]byte(screenshotsJSON), &g.Screenshots)
	json.Unmarshal([]byte(videosJSON), &g.Videos)
	json.Unmarshal([]byte(featuresJSON), &g.Features)
	if g.Tags == nil { g.Tags = []string{} }
	if g.Screenshots == nil { g.Screenshots = []string{} }
	if g.Videos == nil { g.Videos = []string{} }
	if g.Features == nil { g.Features = []string{} }
	// Backward compat: derive video_url from first video
	if g.VideoURL == "" && len(g.Videos) > 0 {
		g.VideoURL = g.Videos[0]
	}
}

func scanGame(row *sql.Row) (*Game, error) {
	g := &Game{}
	var tagsJSON, screenshotsJSON, videosJSON, featuresJSON string
	err := row.Scan(
		&g.ID, &g.Title, &g.Slug, &g.Genre, &g.Price, &g.Discount, &g.Description,
		&g.CoverPath, &g.DeveloperID, &tagsJSON, &g.IsWebGPU, &g.FilePath, &g.EntryFile,
		&screenshotsJSON, &g.VideoURL, &videosJSON, &g.Published,
		&g.ThemeColor, &g.HeaderImage, &g.CustomAbout, &featuresJSON, &g.SysReqMin, &g.SysReqRec,
		&g.CreatedAt, &g.UpdatedAt,
		&g.DeveloperName, &g.AvgRating, &g.ReviewCount, &g.PlayCount,
	)
	if err != nil { return nil, fmt.Errorf("scan game row: %w", err) }
	parseGameJSON(g, tagsJSON, screenshotsJSON, videosJSON, featuresJSON)
	return g, nil
}

func scanGameRow(rows *sql.Rows) (*Game, error) {
	g := &Game{}
	var tagsJSON, screenshotsJSON, videosJSON, featuresJSON string
	err := rows.Scan(
		&g.ID, &g.Title, &g.Slug, &g.Genre, &g.Price, &g.Discount, &g.Description,
		&g.CoverPath, &g.DeveloperID, &tagsJSON, &g.IsWebGPU, &g.FilePath, &g.EntryFile,
		&screenshotsJSON, &g.VideoURL, &videosJSON, &g.Published,
		&g.ThemeColor, &g.HeaderImage, &g.CustomAbout, &featuresJSON, &g.SysReqMin, &g.SysReqRec,
		&g.CreatedAt, &g.UpdatedAt,
		&g.DeveloperName, &g.AvgRating, &g.ReviewCount, &g.PlayCount,
	)
	if err != nil { return nil, fmt.Errorf("scan game rows: %w", err) }
	parseGameJSON(g, tagsJSON, screenshotsJSON, videosJSON, featuresJSON)
	return g, nil
}
