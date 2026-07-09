package handlers

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed playmore-mp.js
var mpScriptContent string

// mpScriptETag is a strong ETag derived from the SDK's content, computed once
// at startup. Because playmore-mp.js lives at a stable URL but its method
// surface changes with each deploy, it must be revalidated rather than blindly
// cached — otherwise a browser/CDN serves an old SDK (missing new lobby-control
// methods) for up to the max-age after a release, silently breaking games.
var mpScriptETag = `"` + hashHex(mpScriptContent) + `"`

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// ServeMPScript serves the embeddable multiplayer client shim at
// GET /playmore-mp.js. Games <script src> this to speak the lobby
// postMessage protocol without hand-writing it.
//
// Caching: a short edge TTL keeps most requests off the origin, while the
// ETag lets browsers and Cloudflare revalidate (cheap 304 when unchanged) so a
// new build propagates within minutes instead of being pinned for a day.
func ServeMPScript(c *gin.Context) {
	c.Header("ETag", mpScriptETag)
	// no-cache = store but revalidate before use; s-maxage caps how long the
	// CDN may serve without revalidating. Deploys go live within ~60s.
	c.Header("Cache-Control", "public, no-cache, s-maxage=60")

	if match := c.GetHeader("If-None-Match"); match == mpScriptETag {
		c.Status(http.StatusNotModified)
		return
	}

	c.Header("Content-Type", "application/javascript; charset=utf-8")
	c.String(http.StatusOK, mpScriptContent)
}
