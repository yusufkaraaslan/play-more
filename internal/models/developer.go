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
	UserID        string          `json:"user_id"`
	DisplayName   string          `json:"display_name"`
	BannerURL     string          `json:"banner_url"`
	ThemeColor    string          `json:"theme_color"`
	ThemePreset   string          `json:"theme_preset"`
	CustomCSS     string          `json:"custom_css"`
	Links         []DeveloperLink `json:"links"`
	About         string          `json:"about"`
	FontHeading   string          `json:"font_heading"`
	FontBody      string          `json:"font_body"`
	FeaturedGames []string        `json:"featured_games"`
	PageLayout    []PageSection   `json:"page_layout"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

type PageSection struct {
	Type    string `json:"type"`    // "about", "games", "featured", "links", "devlogs", "gallery", "spacer", "custom"
	Enabled bool   `json:"enabled"`
	Title   string `json:"title"`   // custom section title override
	Content string `json:"content"` // for "custom" type: markdown content
	Height  int    `json:"height"`  // for "spacer" type: pixels
}

func GetDeveloperPage(userID string) (*DeveloperPage, error) {
	p := &DeveloperPage{}
	var linksJSON, featuredJSON, layoutJSON string
	err := storage.DB.QueryRow(
		`SELECT user_id, display_name, banner_url, theme_color, COALESCE(theme_preset,'steam-dark'), custom_css, links, about,
		        COALESCE(font_heading,''), COALESCE(font_body,''), COALESCE(featured_games,'[]'), COALESCE(page_layout,'[]'), created_at, updated_at
		 FROM developer_pages WHERE user_id = ?`, userID,
	).Scan(&p.UserID, &p.DisplayName, &p.BannerURL, &p.ThemeColor, &p.ThemePreset, &p.CustomCSS, &linksJSON, &p.About,
		&p.FontHeading, &p.FontBody, &featuredJSON, &layoutJSON, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(linksJSON), &p.Links)
	json.Unmarshal([]byte(featuredJSON), &p.FeaturedGames)
	json.Unmarshal([]byte(layoutJSON), &p.PageLayout)
	if p.Links == nil { p.Links = []DeveloperLink{} }
	if p.FeaturedGames == nil { p.FeaturedGames = []string{} }
	if p.PageLayout == nil || len(p.PageLayout) == 0 {
		// Default layout
		p.PageLayout = []PageSection{
			{Type: "about", Enabled: true, Title: "About"},
			{Type: "featured", Enabled: true, Title: "Featured Games"},
			{Type: "games", Enabled: true, Title: "All Games"},
			{Type: "links", Enabled: true, Title: "Links"},
			{Type: "devlogs", Enabled: true, Title: "Devlogs"},
		}
	}
	return p, nil
}

func UpsertDeveloperPage(userID, displayName, bannerURL, themeColor, themePreset, about, fontHeading, fontBody string, links []DeveloperLink, featuredGames []string, pageLayout []PageSection) error {
	linksJSON, _ := json.Marshal(links)
	featuredJSON, _ := json.Marshal(featuredGames)
	layoutJSON, _ := json.Marshal(pageLayout)
	_, err := storage.DB.Exec(
		`INSERT INTO developer_pages (user_id, display_name, banner_url, theme_color, theme_preset, links, about, font_heading, font_body, featured_games, page_layout)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		     display_name = excluded.display_name,
		     banner_url = excluded.banner_url,
		     theme_color = excluded.theme_color,
		     theme_preset = excluded.theme_preset,
		     links = excluded.links,
		     about = excluded.about,
		     font_heading = excluded.font_heading,
		     font_body = excluded.font_body,
		     featured_games = excluded.featured_games,
		     page_layout = excluded.page_layout,
		     updated_at = CURRENT_TIMESTAMP`,
		userID, displayName, bannerURL, themeColor, themePreset, string(linksJSON), about, fontHeading, fontBody, string(featuredJSON), string(layoutJSON),
	)
	return err
}
