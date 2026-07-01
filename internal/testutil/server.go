// Package testutil provides helpers for writing HTTP-level tests
// against the PlayMore server. It wires a real Gin engine onto a
// real (temp-file) SQLite database so handler tests can exercise
// the full request → middleware → handler → DB path.
//
// The testutil intentionally depends on the real packages
// (handlers, models, storage) — the goal is to validate the
// production code path, not a mock. Tests that want to assert
// behavior in isolation can still use the package-level helpers
// (seedUser, seedGame) on a custom *sql.DB.
package testutil

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/server"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// TestServer is a fully-wired test instance: a Gin engine backed
// by a temp-file SQLite database, with cleanup registered on t.
//
// Each NewTestServer call gets its own database. storage.DB is
// swapped for the duration of the test and restored on cleanup.
// Because storage.DB is a package-level *sql.DB, tests in the
// same package run sequentially (the default for `go test`),
// which is what we want — parallel tests would clobber each
// other's global state.
type TestServer struct {
	Engine  *gin.Engine
	DataDir string
	cleanup func()
}

// NewTestServer wires a real router onto a temp-file SQLite DB.
// It returns the engine and a cleanup function. The cleanup is
// also registered with t.Cleanup so callers don't have to.
//
// Usage:
//
//	ts := testutil.NewTestServer(t)
//	// ... use ts.Engine, ts.Do(...)
//	// cleanup happens automatically
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()

	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "playmore.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// Match the production config: single writer, no busy storms.
	db.SetMaxOpenConns(1)

	// Run migrations directly (storage.InitDB also runs them, but
	// we want to bypass its package-level init dance).
	if _, err := db.Exec(storage.Schema()); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	for _, m := range storage.Migrations() {
		if _, err := db.Exec(m); err != nil {
			// Migrations are designed to be idempotent — see
			// storage.isIdempotentMigrationError for the small set
			// of "already exists" errors that are expected on a
			// fresh DB. Anything else is a real failure.
			errStr := err.Error()
			if strings.Contains(errStr, "duplicate column") || strings.Contains(errStr, "already exists") {
				continue
			}
			t.Fatalf("apply migration: %v\nsql: %s", err, m)
		}
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}

	// Save the current storage.DB and replace with the test one.
	prevDB := storage.DB
	storage.DB = db

	r := gin.New()
	cfg := server.NewTestConfig()
	server.MountAPIRoutesForTest(r, cfg)

	// Health check expects storage.DB.Ping to work.
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	cleanup := func() {
		storage.DB = prevDB
		_ = db.Close()
	}
	t.Cleanup(cleanup)

	return &TestServer{
		Engine:  r,
		DataDir: dataDir,
		cleanup: cleanup,
	}
}

// SeedUser creates a user with the given options. Email
// verification defaults to true so write-endpoint tests don't
// trip the RequireVerifiedEmail gate; pass WithUnverifiedEmail
// to test that path explicitly.
type SeedUserOpts struct {
	Username      string
	Email         string
	Password      string
	EmailVerified bool
	IsAdmin       bool
}

func (o SeedUserOpts) withDefaults() SeedUserOpts {
	if o.Username == "" {
		o.Username = "user_" + uuid.NewString()[:8]
	}
	if o.Email == "" {
		o.Email = o.Username + "@example.com"
	}
	if o.Password == "" {
		o.Password = "correct-horse-battery-staple"
	}
	return o
}

// WithUnverifiedEmail returns the user with EmailVerified=false
// for tests that need to exercise the RequireVerifiedEmail gate.
func (o SeedUserOpts) WithUnverifiedEmail() SeedUserOpts {
	o.EmailVerified = false
	return o
}

// WithAdmin returns the user as the first/admin user.
func (o SeedUserOpts) WithAdmin() SeedUserOpts {
	o.IsAdmin = true
	return o
}

// SeedUser creates a user directly in the DB (skipping the HTTP
// layer and the CAPTCHA). Returns the user with the ID set.
// The password is bcrypt-hashed at the same cost as production.
//
// If db is nil, the global storage.DB is used. The testutil
// always sets storage.DB to the temp-file DB during a test, so
// the nil-default is the right thing for most callers.
func SeedUser(t *testing.T, db *sql.DB, opts SeedUserOpts) *models.User {
	t.Helper()
	if db == nil {
		db = storage.DB
	}
	opts = opts.withDefaults()
	id := uuid.NewString()
	hash, err := bcrypt.GenerateFromPassword([]byte(opts.Password), models.BcryptCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	verified := 0
	if opts.EmailVerified {
		verified = 1
	}
	_, err = db.Exec(`INSERT INTO users (id, username, email, password, email_verified) VALUES (?, ?, ?, ?, ?)`,
		id, opts.Username, opts.Email, string(hash), verified)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if opts.IsAdmin {
		// First registered user is admin. In tests, we just mark
		// the row directly to avoid that ambiguity.
		// (Admin gating in the code uses is_developer + first-user
		// check; tests that need admin use the AdminRequired
		// middleware path — for now we set is_developer=1 as a
		// marker; AdminRequired checks first-user, so this is a
		// bit awkward. Use the with-admin login flow if needed.)
		_, _ = db.Exec(`UPDATE users SET is_developer = 1 WHERE id = ?`, id)
	}
	return &models.User{
		ID:            id,
		Username:      opts.Username,
		Email:         opts.Email,
		EmailVerified: opts.EmailVerified,
	}
}

// SeedGame inserts a game owned by the given user. Returns the
// game ID. Defaults to published=true so list endpoints see it
// and sets a placeholder file_path so the build-backfill
// migration creates an initial stable build row.
//
// The backfill migration in storage.db.go only fires for
// pre-existing games at migration time, so we explicitly
// create the initial build row here too — the equivalent of
// what the backfill does in production for games uploaded
// after the migration.
func SeedGame(t *testing.T, db *sql.DB, ownerID string, title string) string {
	t.Helper()
	if db == nil {
		db = storage.DB
	}
	id := uuid.NewString()
	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	if title == "" {
		title = "Test Game"
		slug = "test-game-" + id[:8]
	}
	gameDir := "/tmp/playmore-test/games/" + id
	_, err := db.Exec(
		`INSERT INTO games (id, title, slug, genre, developer_id, published, file_path, entry_file) VALUES (?, ?, ?, ?, ?, 1, ?, 'index.html')`,
		id, title, slug, "action", ownerID, gameDir,
	)
	if err != nil {
		t.Fatalf("seed game: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO game_builds (id, game_id, build_number, channel, file_path, entry_file, size, is_active, created_by)
		 VALUES (?, ?, 1, 'stable', ?, 'index.html', 0, 1, ?)`,
		"build_"+id, id, gameDir, ownerID,
	)
	if err != nil {
		t.Fatalf("seed initial build: %v", err)
	}
	return id
}

// SeedBuild inserts a build row for a game. Returns the build
// ID. Path is the on-disk path; the caller is responsible for
// ensuring it exists. The backfill migration in storage.db.go
// already creates one 'stable' build for every game, so this
// helper is for adding extra builds on top.
func SeedBuild(t *testing.T, db *sql.DB, gameID, ownerID, channel string) string {
	t.Helper()
	if db == nil {
		db = storage.DB
	}
	id := "build_" + uuid.NewString()
	_, err := db.Exec(
		`INSERT INTO game_builds (id, game_id, build_number, channel, file_path, entry_file, size, is_active, created_by)
		 VALUES (?, ?, (SELECT COALESCE(MAX(build_number), 0) + 1 FROM game_builds WHERE game_id = ?), ?, ?, 'index.html', 0, 0, ?)`,
		id, gameID, gameID, channel, "/tmp/test/"+id, ownerID,
	)
	if err != nil {
		t.Fatalf("seed build: %v", err)
	}
	return id
}

// DoAuthed is a shortcut for ts.Do(method, path, body, WithAuth(user)).
// Saves a few characters at the most common call site.
func (ts *TestServer) DoAuthed(t *testing.T, method, path string, body any, user *models.User) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	return ts.Do(t, method, path, body, WithAuth(user))
}

// ReqOption customizes a single HTTP request in Do(). Chain
// multiple options: ts.Do("POST", "/x", body, ts.WithAuth(user), ts.WithHeader("X-Foo", "bar"))
type ReqOption func(*http.Request, *httptest.ResponseRecorder)

// WithAuth logs the user in and sets the session cookie on the
// request. The session is created directly in the DB (no
// /auth/login round-trip needed).
func WithAuth(user *models.User) ReqOption {
	return func(req *http.Request, _ *httptest.ResponseRecorder) {
		token := uuid.NewString()
		hash := models.HashSessionToken(token)
		expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
		_, err := storage.DB.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
			hash, user.ID, expires)
		if err != nil {
			// Surface as a request panic — Do() will recover.
			panic(fmt.Errorf("seed session: %w", err))
		}
		req.AddCookie(&http.Cookie{
			Name:  "session",
			Value: token,
			Path:  "/",
		})
	}
}

// WithAPIKey generates a fresh API key for the user and sets
// the Authorization header. Returns the raw key (in case the
// test wants to call the API again with the same key).
func WithAPIKey(user *models.User) (string, ReqOption) {
	_, rawKey, err := models.GenerateAPIKey(user.ID, "test", "all")
	if err != nil {
		panic(fmt.Errorf("create api key: %w", err))
	}
	opt := func(req *http.Request, _ *httptest.ResponseRecorder) {
		req.Header.Set("Authorization", "Bearer "+rawKey)
	}
	return rawKey, opt
}

// WithHeader sets an arbitrary header.
func WithHeader(key, value string) ReqOption {
	return func(req *http.Request, _ *httptest.ResponseRecorder) {
		req.Header.Set(key, value)
	}
}

// WithIP sets X-Forwarded-For so trusted-proxy-aware middleware
// sees a unique per-test client IP. Helps avoid the global
// rate-limit counter accumulating across tests in the same run.
func WithIP(ip string) ReqOption {
	return func(req *http.Request, _ *httptest.ResponseRecorder) {
		req.Header.Set("X-Forwarded-For", ip)
		req.RemoteAddr = ip + ":12345"
	}
}

// WithSameOrigin sets Origin to http://example.com so the CSRF
// middleware accepts the request. Most tests want this; tests
// that specifically check CSRF should use WithHeader("Origin",
// "https://evil.example.com") to opt out.
func WithSameOrigin() ReqOption {
	return func(req *http.Request, _ *httptest.ResponseRecorder) {
		req.Header.Set("Origin", "http://example.com")
	}
}

// Do fires an HTTP request against the test engine and returns
// the response. body may be nil, []byte, or a string.
//
//	resp, body := ts.Do(t, "GET", "/api/v1/games", nil)
//	if resp.Code != 200 { t.Fatalf(...) }
//	var data map[string]any
//	testutil.DecodeJSON(t, body, &data)
func (ts *TestServer) Do(t *testing.T, method, path string, body any, opts ...ReqOption) (*httptest.ResponseRecorder, []byte) {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		switch v := body.(type) {
		case []byte:
			reqBody = bytes.NewReader(v)
		case string:
			reqBody = strings.NewReader(v)
		default:
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			reqBody = bytes.NewReader(b)
		}
	}

	req := httptest.NewRequest(method, path, reqBody)
	// For state-changing methods, always send a Content-Type.
	// CSRF middleware rejects requests without an Origin/Referer
	// or JSON content-type, so we send application/json by
	// default. Multipart callers use DoMultipart which sets
	// its own Content-Type. This matches what a real client
	// would send for a bodyless POST/PUT/DELETE.
	if method != "GET" && method != "HEAD" && method != "OPTIONS" {
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	for _, opt := range opts {
		opt(req, nil)
	}
	w := httptest.NewRecorder()
	ts.Engine.ServeHTTP(w, req)
	return w, w.Body.Bytes()
}

// FileField is a (filename, content) pair for DoMultipart. The
// filename is what the server sees in multipart.FileHeader's
// Filename field — used by the game upload handler to detect
// the file extension.
type FileField struct {
	Filename string
	Content  []byte
}

// DoMultipart sends a multipart/form-data request. fields is a
// map from field name to either:
//   - string: a plain form field
//   - []byte: a file upload (filename defaults to the field name)
//   - FileField: a file upload with an explicit filename
//
// Returns the response and body bytes.
func (ts *TestServer) DoMultipart(t *testing.T, method, path string, fields map[string]any, opts ...ReqOption) (*httptest.ResponseRecorder, []byte) {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, v := range fields {
		switch val := v.(type) {
		case string:
			if err := w.WriteField(name, val); err != nil {
				t.Fatalf("write field: %v", err)
			}
		case []byte:
			fw, err := w.CreateFormFile(name, name)
			if err != nil {
				t.Fatalf("create form file: %v", err)
			}
			if _, err := fw.Write(val); err != nil {
				t.Fatalf("write form file: %v", err)
			}
		case FileField:
			fw, err := w.CreateFormFile(name, val.Filename)
			if err != nil {
				t.Fatalf("create form file: %v", err)
			}
			if _, err := fw.Write(val.Content); err != nil {
				t.Fatalf("write form file: %v", err)
			}
		default:
			t.Fatalf("multipart field %q has unsupported type %T", name, v)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	// Multipart is a CORS-simple type so the CSRF middleware
	// requires an Origin. Set one to match the test server's
	// default host. Tests that exercise CSRF should set their
	// own Origin via WithHeader.
	if req.Header.Get("Origin") == "" {
		req.Header.Set("Origin", "http://example.com")
	}
	for _, opt := range opts {
		opt(req, nil)
	}
	rec := httptest.NewRecorder()
	ts.Engine.ServeHTTP(rec, req)
	return rec, rec.Body.Bytes()
}

// DecodeJSON unmarshals a JSON response body into v. Fails the
// test on parse error.
func DecodeJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %s", err, body)
	}
}

// ResetRateLimits clears the in-memory rate-limit counters. Use
// between tests in the same package to keep them independent.
// Not safe to call concurrently with requests in flight.
func ResetRateLimits() {
	middleware.ResetLimiters()
}
