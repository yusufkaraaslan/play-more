package server

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/handlers"
)

// TestMountAPIRoutes_OpenAPIDrift is the real-deal drift check:
// mount the actual API routes (the same call New() makes) and
// verify the openapi.yaml in internal/handlers is in sync. This
// is what catches "forgot to document the new endpoint" or
// "removed a route but left the YAML entry". The fixture test
// in handlers/routeindex_test.go is a sanity check; this one
// is the production gate.
func TestMountAPIRoutes_OpenAPIDrift(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := apiConfig{uploadCap: 1 << 20, imageCap: 1 << 20, chunkPutCap: 1 << 20}
	r := gin.New()
	mountAPIRoutes(r.Group("/api/v1"), cfg)
	// We deliberately do NOT mount the legacy /api alias here — the
	// OpenAPI spec documents /api/v1/ as the canonical form. The
	// equivalence test above already verifies both prefixes share
	// the same route table.

	spec, err := handlers.LoadOpenAPISpec()
	if err != nil {
		t.Fatalf("load OpenAPI spec: %v", err)
	}

	live := handlers.AllRoutes(r, "/api/v1")
	report := handlers.CheckDrift(spec, live, "/api/v1")
	if report.HasDrift() {
		t.Errorf("OpenAPI spec drifted from real mounted routes:\n%s",
			formatDriftReport(report))
	}
}

func formatDriftReport(d handlers.DriftReport) string {
	var b strings.Builder
	if len(d.MissingFromYAML) > 0 {
		b.WriteString("routes registered in code but NOT in openapi.yaml:\n")
		for _, r := range d.MissingFromYAML {
			b.WriteString("  " + r.Method + " " + r.Path + "\n")
		}
	}
	if len(d.MissingFromCode) > 0 {
		b.WriteString("routes in openapi.yaml but NOT registered in code:\n")
		for _, r := range d.MissingFromCode {
			b.WriteString("  " + r.Method + " " + r.Path + "\n")
		}
	}
	return b.String()
}
