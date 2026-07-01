package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
)

// ChunkedUploadOptions configures a chunked upload. Size is
// the total size of the file; Path is the local file to read.
// SHA256 is optional — if set, the server verifies the bytes
// match after assembly.
type ChunkedUploadOptions struct {
	Path     string
	Size     int64
	Filename string
	SHA256   string // optional
	// Kind is "new_game" or "reupload". Defaults to "new_game".
	Kind string
	// GameID is required for Kind=reupload.
	GameID string
	// Metadata is the per-kind JSON object. For "new_game",
	// this is the same as GameUploadOptions metadata.
	Metadata map[string]any
	// OnProgress is called after each chunk with the bytes
	// written so far. May be nil.
	OnProgress func(written, total int64)
}

// ChunkedUploadResult is what UploadChunked returns on success.
type ChunkedUploadResult struct {
	UploadID string
	GameID   string
}

// UploadChunked runs the four-step chunked upload protocol.
// Steps: init, PUT chunks, finalize. The caller doesn't need
// to know about retries — this method handles chunk-level
// resume (re-PUTs a chunk up to 3 times on transient errors).
//
// The chunk size matches the server default of 8 MiB. We send
// each chunk in a single PUT and read on the server side via
// io.Copy + ReadAt-style seeking.
func (c *Client) UploadChunked(ctx context.Context, opts ChunkedUploadOptions) (*ChunkedUploadResult, error) {
	if opts.Kind == "" {
		opts.Kind = "new_game"
	}
	if opts.Filename == "" {
		opts.Filename = opts.Path
	}

	// 1. Init
	initBody, _ := json.Marshal(map[string]any{
		"filename": opts.Filename,
		"size":     opts.Size,
		"kind":     opts.Kind,
		"game_id":  opts.GameID,
		"metadata": opts.Metadata,
	})
	req, err := c.newRequest(ctx, "POST", "/uploads/init", bytes.NewReader(initBody), "application/json")
	if err != nil {
		return nil, err
	}
	var initResp struct {
		UploadID  string `json:"upload_id"`
		ChunkSize int64  `json:"chunk_size"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := c.do(req, &initResp); err != nil {
		return nil, fmt.Errorf("init: %w", err)
	}
	if initResp.ChunkSize <= 0 {
		return nil, fmt.Errorf("server returned invalid chunk size: %d", initResp.ChunkSize)
	}

	// 2. PUT chunks
	f, err := os.Open(opts.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var offset int64
	for offset < opts.Size {
		chunkSize := initResp.ChunkSize
		if offset+chunkSize > opts.Size {
			chunkSize = opts.Size - offset
		}
		buf := make([]byte, chunkSize)
		if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
			return nil, fmt.Errorf("read chunk at %d: %w", offset, err)
		}
		if err := c.putChunkWithRetry(ctx, initResp.UploadID, offset, buf, 3); err != nil {
			return nil, fmt.Errorf("put chunk at %d: %w", offset, err)
		}
		offset += chunkSize
		if opts.OnProgress != nil {
			opts.OnProgress(offset, opts.Size)
		}
	}

	// 3. Finalize
	finalBody, _ := json.Marshal(map[string]string{
		"sha256": opts.SHA256,
	})
	req, err = c.newRequest(ctx, "POST", "/uploads/"+initResp.UploadID+"/finalize", bytes.NewReader(finalBody), "application/json")
	if err != nil {
		return nil, err
	}
	var finResp struct {
		GameID string `json:"game_id"`
	}
	if err := c.do(req, &finResp); err != nil {
		return nil, fmt.Errorf("finalize: %w", err)
	}
	return &ChunkedUploadResult{
		UploadID: initResp.UploadID,
		GameID:   finResp.GameID,
	}, nil
}

// putChunkWithRetry retries a chunk PUT up to maxAttempts
// times on transient errors (5xx, network). 4xx is treated as
// permanent and not retried.
func (c *Client) putChunkWithRetry(ctx context.Context, uploadID string, offset int64, chunk []byte, maxAttempts int) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := c.newRequest(ctx, "PUT", fmt.Sprintf("/uploads/%s/chunks?offset=%d", uploadID, offset), bytes.NewReader(chunk), "application/octet-stream")
		if err != nil {
			return err
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = &Error{StatusCode: resp.StatusCode, Body: string(body)}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return lastErr // 4xx — don't retry
		}
	}
	return lastErr
}

// FileSHA256 returns the hex-encoded SHA-256 of the file at
// path. Used to populate ChunkedUploadOptions.SHA256.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ensure multipart is referenced for the multipart imports in
// the games.go counterpart (avoids a stray-unused import
// when someone trims games.go for a quick local edit).
var _ = multipart.ErrMessageTooLarge
