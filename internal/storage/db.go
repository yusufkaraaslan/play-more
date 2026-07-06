package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func InitDB(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "playmore.db")
	var err error
	// _pragma=foreign_keys(1) enables FK enforcement at the driver level so
	// every ON DELETE CASCADE in the schema actually fires. SQLite defaults
	// to OFF; without this, cascade clauses are dead code.
	DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}

	// SetMaxOpenConns(1) forces serialised writes, which is intentional:
	// SQLite is embedded (no network round-trip) and WAL-mode readers do not
	// block. A single writer avoids SQLITE_BUSY retry storms on concurrent
	// INSERT/UPDATE bursts (achievement checks, playtime heartbeats, page-view
	// inserts). Throughput is not a concern for a self-hosted single-node app;
	// correctness under concurrent access is. If you ever need higher write
	// concurrency, pair this with an in-process write queue — do NOT just raise
	// the limit without one.
	DB.SetMaxOpenConns(1)

	// Belt-and-suspenders: also issue the PRAGMA explicitly so we fail loud
	// if the driver ever silently ignores the DSN parameter.
	if _, err := DB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable foreign_keys: %w", err)
	}
	var fkOn int
	if err := DB.QueryRow(`PRAGMA foreign_keys`).Scan(&fkOn); err != nil || fkOn != 1 {
		return fmt.Errorf("foreign_keys PRAGMA didn't stick (on=%d err=%v)", fkOn, err)
	}

	if err := migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return nil
}

func migrate() error {
	if _, err := DB.Exec(schema); err != nil {
		return err
	}
	for _, m := range migrationsAll() {
		if _, err := DB.Exec(m); err != nil {
			// Idempotent migrations re-running over an established DB naturally
			// produce these two errors — that's expected, swallow them silently.
			if isIdempotentMigrationError(err) {
				continue
			}
			// Anything else (disk full, locked table, syntax error in a new
			// migration, FK violation) is a real failure. Surface it so a
			// botched deploy doesn't silently leave the DB in an undefined state.
			return fmt.Errorf("migration failed: %w (sql=%q)", err, m)
		}
	}
	return nil
}

// migrationsAll returns the full set of idempotent migrations.
// Defined as a function (not a package var) so callers can
// iterate it without exposing a mutable slice. Also exposed via
// Migrations() for test code that needs to apply them to a
// temp SQLite.
func migrationsAll() []string {
	return []string{
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
		// Index for the view-dedup hot path (game_id + ip_hash lookup with
		// recent created_at filter). Without this the dedup check would scan
		// all views for a popular game.
		`CREATE INDEX IF NOT EXISTS idx_game_views_game_iphash ON game_views(game_id, ip_hash, created_at)`,
		// Retention cap: page_views older than 90 days are pruned hourly by
		// analytics writer; game_views had no retention. Add an index on the
		// date column so a future retention sweep is cheap.
		`CREATE INDEX IF NOT EXISTS idx_game_views_created ON game_views(created_at)`,
		`CREATE TABLE IF NOT EXISTS upload_sessions (
			id              TEXT PRIMARY KEY,
			user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			game_id         TEXT REFERENCES games(id) ON DELETE CASCADE,
			kind            TEXT NOT NULL,
			filename        TEXT NOT NULL,
			size            INTEGER NOT NULL,
			received_ranges TEXT NOT NULL DEFAULT '[]',
			metadata_json   TEXT NOT NULL DEFAULT '{}',
			sha256_expected TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'open',
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at      DATETIME NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_sessions_user    ON upload_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires ON upload_sessions(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_games_published ON games(published)`,
		`CREATE INDEX IF NOT EXISTS idx_playtime_game ON playtime(game_id)`,
		`ALTER TABLE games ADD COLUMN featured_rank INTEGER DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_games_featured_rank ON games(featured_rank)`,
		// Webhooks (#2) — outbound event subscriptions. The deliveries
		// table retains 7 days; an hourly cleanup pass (see internal/
		// webhook/dispatcher.go) prunes older rows so it never grows
		// unbounded under heavy traffic.
		`CREATE TABLE IF NOT EXISTS webhooks (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			url TEXT NOT NULL,
			events TEXT NOT NULL DEFAULT '[]',
			secret TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			last_triggered_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_webhooks_user ON webhooks(user_id)`,
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			webhook_id TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
			event TEXT NOT NULL,
			payload TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 1,
			response_code INTEGER,
			response_body_excerpt TEXT DEFAULT '',
			delivered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			success INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id, delivered_at)`,
		// Game builds (#4) — per-game internal/beta/stable channels.
		// Each game has up to 5 builds retained on disk; older
		// builds are GC'd by the dispatcher of cron in main.go.
		`CREATE TABLE IF NOT EXISTS game_builds (
			id TEXT PRIMARY KEY,
			game_id TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
			build_number INTEGER NOT NULL,
			channel TEXT NOT NULL CHECK(channel IN ('internal','beta','stable')),
			file_path TEXT NOT NULL,
			entry_file TEXT NOT NULL,
			size INTEGER NOT NULL,
			sha256 TEXT NOT NULL DEFAULT '',
			release_notes TEXT NOT NULL DEFAULT '',
			is_active INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_by TEXT NOT NULL REFERENCES users(id),
			UNIQUE(game_id, build_number))`,
		`CREATE INDEX IF NOT EXISTS idx_game_builds_game_channel ON game_builds(game_id, channel, is_active)`,
		`CREATE INDEX IF NOT EXISTS idx_game_builds_created ON game_builds(game_id, created_at)`,
		// Backfill: every existing game gets one build on the
		// 'stable' channel pointing at its current file_path. The
		// build_number starts at 1 and is_active=1. After this
		// migration, new games get a build row created when they
		// upload (or, for legacy single-shot uploads, this is a
		// no-op fallback).
		`INSERT INTO game_builds (id, game_id, build_number, channel, file_path, entry_file, size, is_active, created_by)
		 SELECT
			'gb_' || id, id, 1, 'stable', file_path, entry_file, 0, 1,
			(SELECT id FROM users WHERE id = games.developer_id LIMIT 1)
		 FROM games
		 WHERE file_path != '' AND NOT EXISTS (SELECT 1 FROM game_builds WHERE game_builds.game_id = games.id)`,
		// Multiplayer lobby (#29) — developer opt-in flag. Gates the
		// lobby UI on the game page and lobby creation over /ws.
		`ALTER TABLE games ADD COLUMN multiplayer BOOLEAN DEFAULT 0`,
	}
}

// isIdempotentMigrationError is true for the specific sqlite errors that
// CREATE-IF-NOT-EXISTS / ADD-COLUMN-already-present migrations produce
// when re-run against a DB that already has the change. Anything else
// is a real problem that should not be silently swallowed.
func isIdempotentMigrationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column") ||
		strings.Contains(msg, "already exists")
}

// IsIdempotentMigrationError is the exported form of
// isIdempotentMigrationError, for the test harness that re-runs
// migrations against a fresh schema and must distinguish benign
// "already exists" errors from real failures.
func IsIdempotentMigrationError(err error) bool {
	return isIdempotentMigrationError(err)
}

// IsUniqueConstraintError checks whether err is a SQLite UNIQUE constraint
// violation. modernc.org/sqlite does not expose structured error codes, so we
// match on the well-known error message substrings that have been stable across
// every SQLite version ever released.
//
// Use this instead of strings.Contains(err.Error(), "UNIQUE") — the bare word
// "UNIQUE" could appear in table names or error contexts unrelated to constraint
// violations.
func IsUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "UNIQUE constraint")
}

// Schema returns the full schema SQL used to initialize a
// PlayMore database. Exposed so tests can apply it to a temp
// SQLite without going through the full InitDB() lifecycle
// (which also sets up the package-level DB pointer).
func Schema() string {
	return schema
}

// Migrations returns the ordered list of idempotent migrations
// (ALTER TABLE / CREATE IF NOT EXISTS) that bring an existing
// schema up to the current version. Exposed so test DBs and
// out-of-band tools (e.g. a one-off upgrade script) can run
// them without going through InitDB's package-level init.
func Migrations() []string {
	return migrationsAll()
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
    multiplayer BOOLEAN DEFAULT 0,
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
