// Package sdk is the hand-written Go SDK for the PlayMore API.
//
// The SDK is intentionally small: it wraps net/http with a few
// ergonomic helpers (auth, error handling, chunked uploads,
// webhook signature verification) and exposes the most-used
// resources as methods on a Client struct. The goal is "fewer
// surprises" rather than "fewer lines" — the public methods
// match the OpenAPI spec at /openapi.yaml one-for-one, so a
// reader of the spec can find the method, and a reader of the
// SDK can find the spec.
package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIBase is the production base URL.
const APIBase = "https://playmore.world/api/v1"

// Client is a PlayMore API client. Auth is via APIKey (Bearer
// token) — the session-cookie auth path is for the SPA and
// not exposed here.
type Client struct {
	// BaseURL is the API root. Defaults to APIBase; can be
	// overridden (e.g. in tests) to point at a local server.
	BaseURL string
	// APIKey is the Bearer token. Generate one from
	// Settings → API Keys in the web UI.
	APIKey string
	// HTTPClient is the underlying transport. Defaults to a
	// 30s-timeout client; override for custom proxy/CA needs.
	HTTPClient *http.Client
}

// New returns a Client pinned to the production API.
func New(apiKey string) *Client {
	return &Client{
		BaseURL:    APIBase,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// newRequest builds an http.Request with the right headers
// (Authorization, Content-Type, Accept) and the API root
// applied to a relative path.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// do executes a request and decodes the JSON response. The
// caller is expected to have already constructed the request
// body and content type. Non-2xx responses are turned into
// *Error values carrying the status code and the JSON error
// message.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode JSON: %w (body=%s)", err, body)
		}
	}
	return nil
}

// Error is returned for non-2xx responses.
type Error struct {
	StatusCode int
	Body       string
}

func (e *Error) Error() string {
	return fmt.Sprintf("playmore: HTTP %d: %s", e.StatusCode, e.Body)
}

// queryValues returns url.Values with the supplied key/value
// pairs set. nil/"" values are omitted. Used to build query
// strings for paginated list endpoints.
func queryValues(pairs map[string]string) url.Values {
	v := url.Values{}
	for k, val := range pairs {
		if val != "" {
			v.Set(k, val)
		}
	}
	return v
}
