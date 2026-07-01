package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// openAPIDocHTML returns the body of /docs (Swagger UI). We serve
// Swagger UI from a CDN (unpkg) and override the CSP to allow
// the script — /docs is a developer-only page, not user-facing,
// so the relaxed CSP is acceptable. The OpenAPI spec is loaded
// from our own /openapi.yaml, so cross-origin script execution
// is limited to the UI bundle and the API spec stays on the
// same origin.
//
// CSP override: default-src 'self' unpkg.com; script-src 'self'
// unpkg.com 'unsafe-inline'; style-src 'self' unpkg.com
// 'unsafe-inline'; img-src 'self' data:; connect-src 'self';
// object-src 'none'.
//
// `unsafe-inline` for script-src is needed because the Swagger UI
// bundle uses an inline init script. The bundle itself is from
// unpkg.com (a trusted CDN).
func openAPIDocHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>PlayMore API Docs</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css">
<style>
body { margin: 0; padding: 0; }
.swagger-ui .info { margin: 30px 0; }
</style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js" crossorigin></script>
<script>
window.onload = () => {
  window.ui = SwaggerUIBundle({
    url: "/openapi.yaml",
    dom_id: "#swagger-ui",
    deepLinking: true,
    presets: [SwaggerUIBundle.presets.apis],
  });
};
</script>
</body>
</html>`
}

// ServeAPIDocs serves the Swagger UI page. If the request comes
// from a browser (Accept: text/html), it serves the Swagger UI.
// Otherwise it returns a small JSON pointer for tooling.
func ServeAPIDocs(c *gin.Context) {
	accept := c.GetHeader("Accept")
	if !strings.Contains(accept, "text/html") {
		c.JSON(http.StatusOK, gin.H{"openapi_spec": "/openapi.yaml"})
		return
	}
	// Override the strict default CSP set by securityHeaders. The
	// /docs page is a developer-only page (not user-facing) and
	// needs to load Swagger UI from unpkg.com.
	c.Header("Content-Security-Policy",
		"default-src 'self' unpkg.com; "+
			"script-src 'self' unpkg.com 'unsafe-inline'; "+
			"style-src 'self' unpkg.com 'unsafe-inline'; "+
			"img-src 'self' data:; "+
			"connect-src 'self'; "+
			"object-src 'none'")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(openAPIDocHTML()))
}
