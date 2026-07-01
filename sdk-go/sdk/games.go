package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
)

// Game is the JSON shape returned by the games endpoints. The
// full set of fields is documented at /openapi.yaml — we
// expose the most-used ones as typed fields and keep the rest
// in Raw for callers that need them.
type Game struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Slug        string         `json:"slug"`
	Genre       string         `json:"genre"`
	Description string         `json:"description"`
	CoverPath   string         `json:"cover_path"`
	DeveloperID string         `json:"developer_id"`
	IsWebGPU    bool           `json:"is_webgpu"`
	Published   bool           `json:"published"`
	Tags        []string       `json:"tags"`
	Screenshots []string       `json:"screenshots"`
	Videos      []string       `json:"videos"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	Raw         map[string]any `json:"-"`
}

// GameUploadOptions configures a new-game upload. The GameFile
// is required; everything else is optional.
type GameUploadOptions struct {
	Title        string
	Genre        string
	Description  string
	Tags         []string
	IsWebGPU     bool
	CoverPath    string
	GameFile     *os.File // caller is responsible for closing
	GameFileName string
}

// UploadGame creates a new game via POST /games. The file is
// sent as multipart/form-data alongside the metadata fields.
// On success, returns the created Game.
func (c *Client) UploadGame(ctx context.Context, opts GameUploadOptions) (*Game, error) {
	if opts.GameFile == nil {
		return nil, fmt.Errorf("GameFile is required")
	}
	defer opts.GameFile.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("title", opts.Title); err != nil {
		return nil, err
	}
	if err := mw.WriteField("genre", opts.Genre); err != nil {
		return nil, err
	}
	if opts.Description != "" {
		mw.WriteField("description", opts.Description)
	}
	if opts.IsWebGPU {
		mw.WriteField("is_webgpu", "true")
	}
	for _, t := range opts.Tags {
		mw.WriteField("tags", t)
	}
	if opts.CoverPath != "" {
		f, err := os.Open(opts.CoverPath)
		if err != nil {
			return nil, fmt.Errorf("open cover: %w", err)
		}
		defer f.Close()
		fw, err := mw.CreateFormFile("cover", opts.CoverPath)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(fw, f); err != nil {
			return nil, err
		}
	}
	name := opts.GameFileName
	if name == "" {
		name = opts.GameFile.Name()
	}
	fw, err := mw.CreateFormFile("game_file", name)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, opts.GameFile); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := c.newRequest(ctx, "POST", "/games", &buf, mw.FormDataContentType())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Game *Game `json:"game"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Game, nil
}

// ListGames returns the first page of published games. For
// pagination beyond the first page, use the page/limit query
// params (added in a future release).
func (c *Client) ListGames(ctx context.Context) ([]*Game, error) {
	req, err := c.newRequest(ctx, "GET", "/games", nil, "")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Games []*Game `json:"games"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Games, nil
}

// GetGame fetches a game by id or slug.
func (c *Client) GetGame(ctx context.Context, id string) (*Game, error) {
	req, err := c.newRequest(ctx, "GET", "/games/"+id, nil, "")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Game *Game `json:"game"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Game, nil
}

// DeleteGame removes a game (owner only).
func (c *Client) DeleteGame(ctx context.Context, id string) error {
	req, err := c.newRequest(ctx, "DELETE", "/games/"+id, nil, "")
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ListDevlogs returns the devlog posts for a game, newest first.
func (c *Client) ListDevlogs(ctx context.Context, gameID string) ([]*Devlog, error) {
	req, err := c.newRequest(ctx, "GET", "/games/"+gameID+"/devlogs", nil, "")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Devlogs []*Devlog `json:"devlogs"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Devlogs, nil
}

// CreateDevlog posts a new devlog entry to a game. title and
// content are required.
func (c *Client) CreateDevlog(ctx context.Context, gameID, title, content string) (*Devlog, error) {
	body, _ := json.Marshal(map[string]string{
		"title":   title,
		"content": content,
	})
	req, err := c.newRequest(ctx, "POST", "/games/"+gameID+"/devlogs", bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Devlog *Devlog `json:"devlog"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Devlog, nil
}

// Devlog is the JSON shape returned by the devlog endpoints.
type Devlog struct {
	ID        string `json:"id"`
	GameID    string `json:"game_id"`
	UserID    string `json:"user_id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// ensure http is referenced (avoids "imported and not used"
// if someone trims the imports). Used by GetGame-style helpers
// that build paths with no body.
var _ = http.MethodGet
