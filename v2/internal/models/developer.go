package models

import (
	"encoding/json"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type DeveloperLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type DeveloperPage struct {
	UserID      string          `json:"user_id"`
	DisplayName string          `json:"display_name"`
	BannerURL   string          `json:"banner_url"`
	ThemeColor  string          `json:"theme_color"`
	CustomCSS   string          `json:"custom_css"`
	Links       []DeveloperLink `json:"links"`
	About       string          `json:"about"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

func GetDeveloperPage(userID string) (*DeveloperPage, error) {
	p := &DeveloperPage{}
	var linksJSON string
	err := storage.DB.QueryRow(
		`SELECT user_id, display_name, banner_url, theme_color, custom_css, links, about, created_at, updated_at
		 FROM developer_pages WHERE user_id = ?`, userID,
	).Scan(&p.UserID, &p.DisplayName, &p.BannerURL, &p.ThemeColor, &p.CustomCSS, &linksJSON, &p.About, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(linksJSON), &p.Links)
	if p.Links == nil {
		p.Links = []DeveloperLink{}
	}
	return p, nil
}

func UpsertDeveloperPage(userID, displayName, bannerURL, themeColor, about string, links []DeveloperLink) error {
	linksJSON, _ := json.Marshal(links)
	_, err := storage.DB.Exec(
		`INSERT INTO developer_pages (user_id, display_name, banner_url, theme_color, links, about)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		     display_name = excluded.display_name,
		     banner_url = excluded.banner_url,
		     theme_color = excluded.theme_color,
		     links = excluded.links,
		     about = excluded.about,
		     updated_at = CURRENT_TIMESTAMP`,
		userID, displayName, bannerURL, themeColor, string(linksJSON), about,
	)
	return err
}
