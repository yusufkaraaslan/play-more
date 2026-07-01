// Package handlers — this file is intentionally a no-op stub.
//
// The old hand-rolled /docs HTML page (APIDocs + apiGroups) has
// been replaced by Swagger UI served via the unpkg CDN bundle
// (see swagger_ui.go for the new ServeAPIDocs handler) and a
// machine-readable OpenAPI spec at /openapi.yaml (see
// openapi_handlers.go). The spec is the single source of truth
// for the API surface; the UI loads it from the same path.
package handlers
