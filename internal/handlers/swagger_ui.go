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
// bundle uses an inline init script (our own static content — the
// page takes no user input, so there is no injection vector for
// it). The CDN bundle itself is pinned by version AND locked with
// a Subresource Integrity (SRI) hash, so a compromised/hijacked
// unpkg (or a MITM) cannot substitute tampered JS: the browser
// refuses to execute a script whose hash doesn't match.
func openAPIDocHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>PlayMore API Docs</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css" integrity="sha384-wxLW6kwyHktdDGr6Pv1zgm/VGJh99lfUbzSn6HNHBENZlCN7W602k9VkGdxuFvPn" crossorigin="anonymous">
<style>
body { margin: 0; padding: 0; }
.swagger-ui .info { margin: 30px 0; }
</style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js" integrity="sha384-wmyclcVGX/WhUkdkATwhaK1X1JtiNrr2EoYJ+diV3vj4v6OC5yCeSu+yW13SYJep" crossorigin="anonymous"></script>
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
