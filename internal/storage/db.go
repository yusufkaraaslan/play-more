package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func InitDB(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "playmore.db")
	var err error
	DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}

	DB.SetMaxOpenConns(1)

	if err := migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return nil
}

func migrate() error {
	if _, err := DB.Exec(schema); err != nil {
		return err
	}
	// Add columns that may be missing from older databases
	migrations := []string{
		`ALTER TABLE developer_pages ADD COLUMN theme_preset TEXT DEFAULT 'steam-dark'`,
		`ALTER TABLE developer_pages ADD COLUMN font_heading TEXT DEFAULT ''`,
		`ALTER TABLE developer_pages ADD COLUMN font_body TEXT DEFAULT ''`,
		`ALTER TABLE developer_pages ADD COLUMN featured_games TEXT DEFAULT '[]'`,
		`ALTER TABLE developer_pages ADD COLUMN page_layout TEXT DEFAULT '[]'`,
		`ALTER TABLE page_views ADD COLUMN device_type TEXT DEFAULT ''`,
		`ALTER TABLE page_views ADD COLUMN os TEXT DEFAULT ''`,
		`ALTER TABLE page_views ADD COLUMN session_id TEXT DEFAULT ''`,
		`ALTER TABLE page_views ADD COLUMN screen_res TEXT DEFAULT ''`,
		`ALTER TABLE page_views ADD COLUMN has_webgpu INTEGER DEFAULT -1`,
		`ALTER TABLE users ADD COLUMN autoplay_media BOOLEAN DEFAULT 0`,
		`ALTER TABLE games ADD COLUMN videos TEXT DEFAULT '[]'`,
		`ALTER TABLE collections ADD COLUMN is_public BOOLEAN DEFAULT 0`,
		`ALTER TABLE collections ADD COLUMN description TEXT DEFAULT ''`,
		`ALTER TABLE collections ADD COLUMN username TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN email_verified BOOLEAN DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL, key_prefix TEXT NOT NULL, key_hash TEXT NOT NULL,
			scopes TEXT DEFAULT 'all', last_used_at DATETIME, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(key_prefix)`,
		// Migrate existing video_url into videos array
		`UPDATE games SET videos = '["' || video_url || '"]' WHERE video_url != '' AND videos = '[]'`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_id TEXT NOT NULL,
			action TEXT NOT NULL,
			target_type TEXT,
			target_id TEXT,
			ip TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
	}
	for _, m := range migrations {
		DB.Exec(m) // ignore errors (column already exists)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id          TEXT PRIMARY KEY,
    username    TEXT UNIQUE NOT NULL,
    email       TEXT UNIQUE NOT NULL,
    password    TEXT NOT NULL,
    avatar_url  TEXT DEFAULT '',
    bio         TEXT DEFAULT '',
    is_developer BOOLEAN DEFAULT 0,
    banner_url  TEXT DEFAULT '',
    theme_color TEXT DEFAULT '#66c0f4',
    links       TEXT DEFAULT '[]',
    autoplay_media BOOLEAN DEFAULT 0,
    email_verified BOOLEAN DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS games (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    slug        TEXT UNIQUE NOT NULL,
    genre       TEXT NOT NULL,
    price       REAL DEFAULT 0,
    discount    INTEGER DEFAULT 0,
    description TEXT DEFAULT '',
    cover_path  TEXT DEFAULT '',
    developer_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tags        TEXT DEFAULT '[]',
    is_webgpu   BOOLEAN DEFAULT 0,
    file_path   TEXT DEFAULT '',
    entry_file  TEXT DEFAULT 'index.html',
    screenshots TEXT DEFAULT '[]',
    video_url   TEXT DEFAULT '',
    videos      TEXT DEFAULT '[]',
    published   BOOLEAN DEFAULT 1,
    theme_color TEXT DEFAULT '',
    header_image TEXT DEFAULT '',
    custom_about TEXT DEFAULT '',
    features    TEXT DEFAULT '[]',
    sys_req_min TEXT DEFAULT '',
    sys_req_rec TEXT DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS reviews (
    id          TEXT PRIMARY KEY,
    game_id     TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rating      INTEGER NOT NULL CHECK(rating >= 1 AND rating <= 5),
    text        TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(game_id, user_id)
);

CREATE TABLE IF NOT EXISTS library (
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    game_id     TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    added_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, game_id)
);

CREATE TABLE IF NOT EXISTS wishlist (
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    game_id     TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    added_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, game_id)
);

CREATE TABLE IF NOT EXISTS playtime (
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    game_id       TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    total_seconds REAL DEFAULT 0,
    last_played   DATETIME,
    play_count    INTEGER DEFAULT 0,
    PRIMARY KEY (user_id, game_id)
);

CREATE TABLE IF NOT EXISTS activity (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    game_id     TEXT,
    detail      TEXT DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS developer_pages (
    user_id        TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    display_name   TEXT DEFAULT '',
    banner_url     TEXT DEFAULT '',
    theme_color    TEXT DEFAULT '#66c0f4',
    theme_preset   TEXT DEFAULT 'steam-dark',
    custom_css     TEXT DEFAULT '',
    links          TEXT DEFAULT '[]',
    about          TEXT DEFAULT '',
    font_heading   TEXT DEFAULT '',
    font_body      TEXT DEFAULT '',
    featured_games TEXT DEFAULT '[]',
    page_layout    TEXT DEFAULT '[]',
    created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS devlogs (
    id          TEXT PRIMARY KEY,
    game_id     TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    content     TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS follows (
    follower_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (follower_id, followed_id)
);

CREATE TABLE IF NOT EXISTS collections (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT DEFAULT '',
    game_ids    TEXT DEFAULT '[]',
    is_public   BOOLEAN DEFAULT 0,
    username    TEXT DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS notifications (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    message     TEXT NOT NULL,
    game_id     TEXT DEFAULT '',
    from_user   TEXT DEFAULT '',
    read        BOOLEAN DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS game_views (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id     TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    user_id     TEXT DEFAULT '',
    ip_hash     TEXT DEFAULT '',
    referrer    TEXT DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS comments (
    id          TEXT PRIMARY KEY,
    devlog_id   TEXT NOT NULL REFERENCES devlogs(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id   TEXT DEFAULT '',
    text        TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_comments_devlog ON comments(devlog_id);

CREATE TABLE IF NOT EXISTS user_achievements (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    achievement_id TEXT NOT NULL,
    unlocked_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, achievement_id)
);

CREATE TABLE IF NOT EXISTS page_views (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    path          TEXT NOT NULL,
    method        TEXT NOT NULL,
    ip_hash       TEXT DEFAULT '',
    user_agent    TEXT DEFAULT '',
    referrer      TEXT DEFAULT '',
    user_id       TEXT DEFAULT '',
    status_code   INTEGER DEFAULT 200,
    response_ms   INTEGER DEFAULT 0,
    device_type   TEXT DEFAULT '',
    os            TEXT DEFAULT '',
    session_id    TEXT DEFAULT '',
    screen_res    TEXT DEFAULT '',
    has_webgpu    INTEGER DEFAULT -1,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS api_keys (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    key_prefix  TEXT NOT NULL,
    key_hash    TEXT NOT NULL,
    scopes      TEXT DEFAULT 'all',
    last_used_at DATETIME,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(key_prefix);

CREATE TABLE IF NOT EXISTS email_tokens (
    token       TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    expires_at  DATETIME NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_email_tokens_user ON email_tokens(user_id, type);
CREATE INDEX IF NOT EXISTS idx_page_views_date ON page_views(created_at);
CREATE INDEX IF NOT EXISTS idx_page_views_path ON page_views(path);
CREATE INDEX IF NOT EXISTS idx_page_views_date_path ON page_views(created_at, path);
CREATE INDEX IF NOT EXISTS idx_page_views_date_status ON page_views(created_at, status_code);
CREATE INDEX IF NOT EXISTS idx_game_views_game ON game_views(game_id);
CREATE INDEX IF NOT EXISTS idx_game_views_date ON game_views(game_id, created_at);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, read);
CREATE INDEX IF NOT EXISTS idx_devlogs_game ON devlogs(game_id);
CREATE INDEX IF NOT EXISTS idx_follows_followed ON follows(followed_id);

CREATE TABLE IF NOT EXISTS audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    actor_id    TEXT NOT NULL,
    action      TEXT NOT NULL,
    target_type TEXT,
    target_id   TEXT,
    ip          TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor ON audit_log(actor_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log(created_at);
CREATE VIRTUAL TABLE IF NOT EXISTS games_fts USING fts5(title, description, tags, content='games', content_rowid='rowid');

CREATE TRIGGER IF NOT EXISTS games_fts_insert AFTER INSERT ON games BEGIN
    INSERT INTO games_fts(rowid, title, description, tags) VALUES (new.rowid, new.title, new.description, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS games_fts_update AFTER UPDATE ON games BEGIN
    INSERT INTO games_fts(games_fts, rowid, title, description, tags) VALUES ('delete', old.rowid, old.title, old.description, old.tags);
    INSERT INTO games_fts(rowid, title, description, tags) VALUES (new.rowid, new.title, new.description, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS games_fts_delete AFTER DELETE ON games BEGIN
    INSERT INTO games_fts(games_fts, rowid, title, description, tags) VALUES ('delete', old.rowid, old.title, old.description, old.tags);
END;

CREATE INDEX IF NOT EXISTS idx_games_genre ON games(genre);
CREATE INDEX IF NOT EXISTS idx_games_developer ON games(developer_id);
CREATE INDEX IF NOT EXISTS idx_reviews_game ON reviews(game_id);
CREATE INDEX IF NOT EXISTS idx_activity_user ON activity(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
`
