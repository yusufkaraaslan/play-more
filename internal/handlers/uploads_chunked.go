package handlers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// ChunkSize is the server-recommended chunk size returned by /init.
// Clients should not exceed (ChunkSize + 1 MiB headroom) in a single PUT.
const ChunkSize int64 = 8 << 20 // 8 MiB

// SessionTTL is the lifetime of an upload session from creation.
const SessionTTL = 24 * time.Hour

// sessionLocks gives each upload_id its own mutex so concurrent PUTs for the
// same upload serialize on the read-modify-write of received_ranges.
var sessionLocks sync.Map

func sessionLock(id string) *sync.Mutex {
	v, _ := sessionLocks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// initReq is the JSON body of POST /api/uploads/init.
type initReq struct {
	Filename string          `json:"filename"`
	Size     int64           `json:"size"`
	Kind     string          `json:"kind"`
	GameID   string          `json:"game_id,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// initResp is the JSON response body of /init.
type initResp struct {
	UploadID  string    `json:"upload_id"`
	ChunkSize int64     `json:"chunk_size"`
	ExpiresAt time.Time `json:"expires_at"`
}

// gameMetadata is the schema of the `metadata` field for kind=new_game.
type gameMetadata struct {
	Title       string   `json:"title"`
	Genre       string   `json:"genre"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	IsWebGPU    bool     `json:"is_webgpu,omitempty"`
}

// InitUpload handles POST /api/uploads/init.
func InitUpload(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var req initReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	// Validate filename
	safe := storage.SanitizeFileName(req.Filename)
	if safe == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
		return
	}
	if req.Size <= 0 || req.Size > storage.MaxFileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "size out of range"})
		return
	}

	// Validate kind + companion fields
	s := &models.UploadSession{
		UserID:   user.ID,
		Kind:     req.Kind,
		Filename: safe,
		Size:     req.Size,
	}
	switch req.Kind {
	case "new_game":
		if req.GameID != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "game_id must be absent for kind=new_game"})
			return
		}
		if len(req.Metadata) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "metadata required for kind=new_game"})
			return
		}
		var meta gameMetadata
		if err := json.Unmarshal(req.Metadata, &meta); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid metadata"})
			return
		}
		if meta.Title == "" || meta.Genre == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title and genre required in metadata"})
			return
		}
		s.MetadataJSON = string(req.Metadata)
	case "reupload":
		if req.GameID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "game_id required for kind=reupload"})
			return
		}
		if len(req.Metadata) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "metadata must be absent for kind=reupload"})
			return
		}
		// Existence + ownership check: 404 on either miss
		g, err := models.GetGameByID(req.GameID)
		if err != nil || g == nil || g.DeveloperID != user.ID {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		s.GameID = sql.NullString{String: req.GameID, Valid: true}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid kind"})
		return
	}

	if err := models.CreateUploadSession(s, SessionTTL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}
	if err := storage.CreatePartial(s.ID, s.Size); err != nil {
		_ = models.DeleteUploadSession(s.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate storage"})
		return
	}

	c.JSON(http.StatusOK, initResp{
		UploadID:  s.ID,
		ChunkSize: ChunkSize,
		ExpiresAt: s.ExpiresAt,
	})
}

// putChunkResp is the JSON returned from a successful PUT chunk.
type putChunkResp struct {
	ReceivedBytes int64 `json:"received_bytes"`
}

// PutChunk handles PUT /api/uploads/:upload_id/chunks?offset=N.
//
// Body is the raw chunk bytes (application/octet-stream). The route layer
// wraps the body in http.MaxBytesReader at (ChunkSize + 1 MiB headroom), so
// oversized chunks are rejected before reaching this handler.
//
// Concurrency: a per-session mutex serializes the read-modify-write of
// received_ranges. Without it, two concurrent PUTs could race and lose
// range updates.
func PutChunk(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id := c.Param("upload_id")
	offsetStr := c.Query("offset")
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil || offset < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid offset"})
		return
	}

	// Per-session lock for the read-modify-write of received_ranges.
	lock := sessionLock(id)
	lock.Lock()
	defer lock.Unlock()

	s, err := models.GetUploadSession(id)
	if err == sql.ErrNoRows || s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session lookup failed"})
		return
	}
	if s.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if s.Status != "open" {
		c.JSON(http.StatusConflict, gin.H{"error": "session not open"})
		return
	}

	// Body is already capped by http.MaxBytesReader at the route layer.
	buf, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "chunk too large"})
		return
	}
	n := int64(len(buf))
	if n == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty body"})
		return
	}
	if offset+n > s.Size {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chunk exceeds declared size"})
		return
	}

	if err := storage.WritePartialAt(id, offset, buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write failed"})
		return
	}

	newRanges := models.AddRange(s.ReceivedRanges, offset, offset+n)
	if _, err := models.UpdateReceivedRanges(id, newRanges); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "range update failed"})
		return
	}

	c.JSON(http.StatusOK, putChunkResp{ReceivedBytes: models.ReceivedBytes(newRanges)})
}

// statusResp is the JSON returned from GET /api/uploads/:upload_id.
type statusResp struct {
	Size           int64      `json:"size"`
	ReceivedRanges [][2]int64 `json:"received_ranges"`
	ExpiresAt      time.Time  `json:"expires_at"`
	Status         string     `json:"status"`
}

// GetUploadStatus handles GET /api/uploads/:upload_id.
func GetUploadStatus(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id := c.Param("upload_id")
	s, err := models.GetUploadSession(id)
	if err == sql.ErrNoRows || s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}
	if s.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	c.JSON(http.StatusOK, statusResp{
		Size:           s.Size,
		ReceivedRanges: s.ReceivedRanges,
		ExpiresAt:      s.ExpiresAt,
		Status:         s.Status,
	})
}

// CancelUpload handles DELETE /api/uploads/:upload_id.
func CancelUpload(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id := c.Param("upload_id")

	// Acquire the session lock to prevent racing with a concurrent PUT/finalize.
	lock := sessionLock(id)
	lock.Lock()
	defer lock.Unlock()

	s, err := models.GetUploadSession(id)
	if err == sql.ErrNoRows || s == nil {
		c.Status(http.StatusNoContent) // idempotent — already gone
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}
	if s.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	_ = storage.DeletePartial(id)
	_ = models.DeleteUploadSession(id)
	sessionLocks.Delete(id)
	c.Status(http.StatusNoContent)
}

// finalizeReq is the JSON body of POST /api/uploads/:upload_id/finalize.
type finalizeReq struct {
	SHA256 string `json:"sha256,omitempty"`
}

// finalizeResp is the success body for kind=new_game.
type finalizeResp struct {
	GameID string `json:"game_id"`
}

// FinalizeUpload handles POST /api/uploads/:upload_id/finalize.
//
// State transitions:
//   - On entry, the session must be status='open' with all bytes received.
//   - MarkFinalizing atomically flips open→finalizing (one DB UPDATE WHERE
//     status='open'). Only one caller can win.
//   - On success: status='done' briefly, then row is deleted + partial file
//     removed.
//   - On any post-MarkFinalizing failure: status='failed' (row preserved as
//     audit trail; GC will sweep eventually), partial file removed.
func FinalizeUpload(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id := c.Param("upload_id")

	var req finalizeReq
	_ = c.ShouldBindJSON(&req) // body is optional

	// Acquire session lock to prevent races with PUT/cancel.
	lock := sessionLock(id)
	lock.Lock()
	defer lock.Unlock()
	// Note: don't sessionLocks.Delete(id) here — keep the entry briefly so a
	// late retry doesn't create a new lock and race; the GC pass handles cleanup.

	s, err := models.GetUploadSession(id)
	if err == sql.ErrNoRows || s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}
	if s.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if !models.IsComplete(s.ReceivedRanges, s.Size) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "upload incomplete"})
		return
	}

	// Atomic open → finalizing. Whoever wins owns the rest of this function.
	won, err := models.MarkFinalizing(id, req.SHA256)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "state update failed"})
		return
	}
	if !won {
		c.JSON(http.StatusConflict, gin.H{"error": "session not open"})
		return
	}

	// On any error from here, mark failed + clean up partial bytes.
	failFinalize := func(code int, msg string) {
		_ = models.MarkStatus(id, "failed")
		_ = storage.DeletePartial(id)
		c.JSON(code, gin.H{"error": msg})
	}

	// Optional sha256 verification.
	if req.SHA256 != "" {
		hf, err := storage.OpenPartial(id)
		if err != nil {
			failFinalize(http.StatusInternalServerError, "open partial failed")
			return
		}
		h := sha256.New()
		if _, err := io.Copy(h, hf); err != nil {
			hf.Close()
			failFinalize(http.StatusInternalServerError, "hash failed")
			return
		}
		hf.Close()
		got := hex.EncodeToString(h.Sum(nil))
		if got != strings.ToLower(req.SHA256) {
			failFinalize(http.StatusBadRequest, "sha256 mismatch")
			return
		}
	}

	// Open the partial again for ZIP extraction (also a ReaderAt).
	f, err := storage.OpenPartial(id)
	if err != nil {
		failFinalize(http.StatusInternalServerError, "open partial failed")
		return
	}
	defer f.Close()

	var meta gameMetadata
	if s.Kind == "new_game" {
		if err := json.Unmarshal([]byte(s.MetadataJSON), &meta); err != nil {
			failFinalize(http.StatusInternalServerError, "metadata decode failed")
			return
		}
	}

	// Decide game target: existing (reupload) or new (created here).
	var targetGameID string
	var newGame *models.Game // only set on kind=new_game; rollback target on failure
	if s.Kind == "reupload" {
		targetGameID = s.GameID.String
		// Wipe old game dir before extracting so leftover files don't shadow new ones.
		_ = storage.DeleteGameFiles(targetGameID)
	} else {
		// new_game — create the row using the existing CreateGame signature.
		price := 0.0
		g, err := models.CreateGame(meta.Title, meta.Genre, meta.Description, user.ID, price, meta.Tags, meta.IsWebGPU)
		if err != nil || g == nil {
			failFinalize(http.StatusInternalServerError, "create game failed")
			return
		}
		newGame = g
		targetGameID = g.ID
	}

	// Extract or place the uploaded file.
	lowerName := strings.ToLower(s.Filename)
	var entryFile string
	switch {
	case strings.HasSuffix(lowerName, ".zip"):
		ef, err := storage.ExtractZipFromReader(targetGameID, f, s.Size)
		if err != nil {
			if newGame != nil {
				_ = newGame.Delete()
			}
			failFinalize(http.StatusBadRequest, "invalid game file")
			return
		}
		entryFile = ef
	case strings.HasSuffix(lowerName, ".html"), strings.HasSuffix(lowerName, ".htm"):
		if _, err := f.Seek(0, 0); err != nil {
			if newGame != nil {
				_ = newGame.Delete()
			}
			failFinalize(http.StatusInternalServerError, "seek partial failed")
			return
		}
		htmlData, err := io.ReadAll(f)
		if err != nil {
			if newGame != nil {
				_ = newGame.Delete()
			}
			failFinalize(http.StatusInternalServerError, "read partial failed")
			return
		}
		if err := storage.SaveGameFile(targetGameID, s.Filename, htmlData); err != nil {
			if newGame != nil {
				_ = newGame.Delete()
			}
			failFinalize(http.StatusInternalServerError, "save file failed")
			return
		}
		entryFile = s.Filename
	default:
		if newGame != nil {
			_ = newGame.Delete()
		}
		failFinalize(http.StatusBadRequest, "game file must be .html, .htm, or .zip")
		return
	}

	// Update the game record with files info using the existing instance method.
	// For reupload, fetch the Game first (ownership was already verified at init time).
	var gameForUpdate *models.Game
	if newGame != nil {
		gameForUpdate = newGame
	} else {
		gameForUpdate, _ = models.GetGameByID(targetGameID)
	}
	if gameForUpdate != nil {
		if err := gameForUpdate.UpdateFiles(storage.GameDir(targetGameID), entryFile); err != nil {
			// File bytes are on disk but record update failed — for new_game,
			// roll back the row and game dir; for reupload, the dir holds the
			// new bytes but file_path/entry_file may be stale (best-effort cleanup).
			if newGame != nil {
				_ = storage.DeleteGameFiles(targetGameID)
				_ = newGame.Delete()
			}
			failFinalize(http.StatusInternalServerError, "update game files failed")
			return
		}
	}

	// Success — clean up session + partial.
	_ = models.MarkStatus(id, "done")
	_ = storage.DeletePartial(id)
	_ = models.DeleteUploadSession(id)

	if s.Kind == "new_game" {
		c.JSON(http.StatusOK, finalizeResp{GameID: targetGameID})
		return
	}
	c.Status(http.StatusNoContent)
}
