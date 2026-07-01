package handlers

import (
	_ "embed"
	"net/http"
	"path/filepath"
	"runtime"

	"github.com/gin-gonic/gin"
)

// openAPISpecYAML is the hand-written OpenAPI 3.0.3 spec for the
// /api/v1 surface. Embedded into the binary at build time so the
// single-file deployment story is preserved — no external file
// needed at runtime.
//
//go:embed openapi.yaml
var openAPISpecYAML []byte

// openAPIPath returns the on-disk location of openapi.yaml.
// Resolved relative to this source file (not the process working
// directory) so tests in other packages can find the file when
// they call LoadOpenAPISpec.
//
// The go:embed directive above does not expose a path at runtime;
// we use runtime.Caller to find our own source file location and
// look next to it.
func openAPIPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "openapi.yaml")
}

// ServeOpenAPISpec serves the hand-written openapi.yaml at
// GET /openapi.yaml. The Content-Type is `application/yaml` so
// SDK generators, Swagger UI, and curl all pick it up correctly.
func ServeOpenAPISpec(c *gin.Context) {
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", openAPISpecYAML)
}
