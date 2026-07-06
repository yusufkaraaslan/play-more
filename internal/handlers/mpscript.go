package handlers

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed playmore-mp.js
var mpScriptContent string

// ServeMPScript serves the embeddable multiplayer client shim at
// GET /playmore-mp.js. Games <script src> this to speak the lobby
// postMessage protocol without hand-writing it. Cached for a day —
// the file is versioned by content, not URL, so keep it modest.
func ServeMPScript(c *gin.Context) {
	c.Header("Content-Type", "application/javascript; charset=utf-8")
	c.Header("Cache-Control", "public, max-age=86400")
	c.String(http.StatusOK, mpScriptContent)
}
