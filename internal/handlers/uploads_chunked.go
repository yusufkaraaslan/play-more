package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
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
