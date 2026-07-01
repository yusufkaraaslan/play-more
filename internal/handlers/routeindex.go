package handlers

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	yaml "github.com/goccy/go-yaml"
)

// RouteSpec is the minimal view of a registered route used to compare
// the live Gin route table against the hand-written OpenAPI spec.
// Anything more verbose (path params, handler names, middleware) is
// not needed for drift detection — we only need method+path.
type RouteSpec struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}

// openAPI is the projection of an OpenAPI 3.0 document used by the
// drift + schema checks. Paths keeps the method+path table for
// drift; Components lets the schema check resolve $refs.
type openAPI struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]any `yaml:"schemas"`
	} `yaml:"components"`
}

// LoadOpenAPISpec reads the OpenAPI YAML from disk (relative to
// the working directory of the test) and returns the parsed
// document. Tests use this so the bytes the drift check sees are
// the same bytes the //go:embed in openapi_handlers.go will
// include in the binary.
func LoadOpenAPISpec() (*openAPI, error) {
	data, err := os.ReadFile(openAPIPath())
	if err != nil {
		return nil, fmt.Errorf("read openapi.yaml: %w", err)
	}
	var spec openAPI
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse openapi.yaml: %w", err)
	}
	return &spec, nil
}

// AllRoutes extracts the (method, path) pairs registered on a
// Gin engine under the given prefix. The prefix is stripped from
// the path so routes registered at /api/v1/foo appear as /foo —
// matching the OpenAPI convention where paths are listed relative
// to the server root.
//
// Pass an empty prefix to get the full route table. Pass "/api/v1"
// to get only the canonical v1 routes. The drift test uses the
// latter, then ignores non-/api/* routes that are mounted on the
// root engine (e.g. /health, /docs, /play/:id).
func AllRoutes(r *gin.Engine, prefix string) []RouteSpec {
	var out []RouteSpec
	for _, ri := range r.Routes() {
		if ri.Path == "" {
			continue
		}
		if prefix != "" {
			if ri.Path != prefix && !strings.HasPrefix(ri.Path, prefix+"/") {
				continue
			}
			// Also exclude routes under a longer prefix sharing our root
			// (e.g. when collecting /api, exclude /api/v1/...). We don't
			// need that here because the server only mounts two API
			// prefixes (/api/v1 and /api) and we always want both — the
			// /openapi.yaml documents the canonical /api/v1/ form.
		}
		spec := RouteSpec{
			Method: strings.ToUpper(ri.Method),
			Path:   ri.Path,
		}
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// DriftReport describes the difference between a live route table
// and the OpenAPI YAML. Used by the drift test to print actionable
// output — devs need to know "this route exists in code but is not
// in the YAML, add it" or vice versa.
type DriftReport struct {
	MissingFromYAML []RouteSpec // registered in code, not documented
	MissingFromCode []RouteSpec // documented, but not registered
}

func (d DriftReport) HasDrift() bool {
	return len(d.MissingFromYAML) > 0 || len(d.MissingFromCode) > 0
}

// httpMethods is the set of valid OpenAPI 3.0 method keys at a path
// item. Anything else in a path's map (e.g. `parameters`, `summary`,
// `description`, `servers`) is metadata, not a route — the drift
// check must ignore it.
var httpMethods = map[string]bool{
	"get":     true,
	"post":    true,
	"put":     true,
	"delete":  true,
	"patch":   true,
	"head":    true,
	"options": true,
	"trace":   true,
}

// CheckDrift compares the live routes (after stripping stripPrefix
// from each path) against the paths in the OpenAPI YAML. Returns a
// DriftReport. The matching is method+path: a route is "documented"
// if `paths[path][method]` exists in the YAML (regardless of whether
// the operation has a full request/response definition — having the
// method key is enough to keep the table from drifting).
//
// stripPrefix exists because the OpenAPI spec uses server-relative
// paths (`/auth/login`) under a `servers: [{url: "/api/v1"}]` entry,
// while the live Gin routes are absolute (`/api/v1/auth/login`).
// Passing "/api/v1" here puts both sides in the same coordinate
// system for comparison.
//
// Path-param syntax differs between Gin (`:id`) and OpenAPI (`{id}`);
// we normalize Gin paths to OpenAPI form before comparison so a
// route like `/games/:id` matches the spec's `/games/{id}`.
func CheckDrift(spec *openAPI, live []RouteSpec, stripPrefix string) DriftReport {
	docSet := map[string]bool{}
	for path, methods := range spec.Paths {
		for method := range methods {
			// Skip non-method keys (parameters, summary, etc.).
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			docSet[strings.ToUpper(method)+" "+path] = true
		}
	}
	liveSet := map[string]bool{}
	for _, r := range live {
		normalized := r.Path
		if stripPrefix != "" {
			normalized = strings.TrimPrefix(r.Path, stripPrefix)
			if normalized == "" {
				normalized = "/"
			}
		}
		// Convert Gin path-param syntax `:foo` → OpenAPI `{foo}`.
		// We walk byte-by-byte so a literal `:` in a path segment
		// (none exist today, but defensively) is only converted when
		// followed by an identifier character.
		normalized = normalizeGinPathToOpenAPI(normalized)
		liveSet[r.Method+" "+normalized] = true
	}
	report := DriftReport{}
	for k := range liveSet {
		if !docSet[k] {
			parts := strings.SplitN(k, " ", 2)
			report.MissingFromYAML = append(report.MissingFromYAML, RouteSpec{
				Method: parts[0], Path: parts[1],
			})
		}
	}
	for k := range docSet {
		if !liveSet[k] {
			parts := strings.SplitN(k, " ", 2)
			report.MissingFromCode = append(report.MissingFromCode, RouteSpec{
				Method: parts[0], Path: parts[1],
			})
		}
	}
	sort.Slice(report.MissingFromYAML, func(i, j int) bool {
		if report.MissingFromYAML[i].Path != report.MissingFromYAML[j].Path {
			return report.MissingFromYAML[i].Path < report.MissingFromYAML[j].Path
		}
		return report.MissingFromYAML[i].Method < report.MissingFromYAML[j].Method
	})
	sort.Slice(report.MissingFromCode, func(i, j int) bool {
		if report.MissingFromCode[i].Path != report.MissingFromCode[j].Path {
			return report.MissingFromCode[i].Path < report.MissingFromCode[j].Path
		}
		return report.MissingFromCode[i].Method < report.MissingFromCode[j].Method
	})
	return report
}

// normalizeGinPathToOpenAPI rewrites a Gin path template like
// `/games/:id/screenshots/:index` into OpenAPI form
// `/games/{id}/screenshots/{index}`. A leading colon followed by
// an identifier character starts a parameter; literal colons in
// path segments (none currently exist) are left alone.
func normalizeGinPathToOpenAPI(p string) string {
	if !strings.Contains(p, ":") {
		return p
	}
	var b strings.Builder
	b.Grow(len(p) + 8)
	i := 0
	for i < len(p) {
		c := p[i]
		if c != ':' {
			b.WriteByte(c)
			i++
			continue
		}
		// Found ':' — start a parameter.
		j := i + 1
		for j < len(p) && (isAlphaNumeric(p[j]) || p[j] == '_') {
			j++
		}
		if j == i+1 {
			// ':' not followed by an identifier — leave it literal.
			b.WriteByte(':')
			i++
			continue
		}
		b.WriteByte('{')
		b.WriteString(p[i+1 : j])
		b.WriteByte('}')
		i = j
	}
	return b.String()
}

func isAlphaNumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// SchemaIssue is one spot where the OpenAPI document is not
// described well enough to generate a client from: a missing
// success-response schema, a request body without a schema, or a
// $ref that doesn't resolve.
type SchemaIssue struct {
	Method string
	Path   string
	Detail string
}

func (s SchemaIssue) String() string {
	return fmt.Sprintf("%s %s: %s", s.Method, s.Path, s.Detail)
}

// CheckSchemaCompleteness upgrades the drift check from "the
// method+path exists in the YAML" to "the operation is actually
// described". Applied to EVERY operation: responses must exist; any
// declared content block must carry a schema; any declared
// requestBody must carry a content schema; and every $ref must
// resolve to a defined component.
//
// requireResponseSchema selects the operations that must ALSO carry
// a full success-response schema (every 2xx/non-204 response gets a
// content schema). The developer/SDK surface (webhooks, builds,
// api-keys, the game + upload endpoints the SDK targets) passes this
// predicate; the rest of the app is held to ref-integrity only, so
// legacy endpoints documented with prose responses don't block the
// gate while the codegen-facing surface is fully typed.
//
// Returns the list of issues (empty when complete), sorted for
// deterministic test output.
func CheckSchemaCompleteness(spec *openAPI, requireResponseSchema func(path string) bool) []SchemaIssue {
	defined := map[string]bool{}
	for name := range spec.Components.Schemas {
		defined["#/components/schemas/"+name] = true
	}

	var issues []SchemaIssue
	for _, path := range sortedKeys(spec.Paths) {
		methods := spec.Paths[path]
		requireResp := requireResponseSchema != nil && requireResponseSchema(path)
		for _, method := range sortedKeys(methods) {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			m := strings.ToUpper(method)
			op, ok := methods[method].(map[string]any)
			if !ok {
				issues = append(issues, SchemaIssue{m, path, "operation is not a mapping"})
				continue
			}

			// Responses must exist and be non-empty.
			responses, _ := op["responses"].(map[string]any)
			if len(responses) == 0 {
				issues = append(issues, SchemaIssue{m, path, "no responses documented"})
			}
			for _, code := range sortedKeys(responses) {
				resp, ok := responses[code].(map[string]any)
				if !ok {
					continue
				}
				content, hasContent := resp["content"].(map[string]any)
				if requireResp && is2xxWithBody(code) && (!hasContent || len(content) == 0) {
					issues = append(issues, SchemaIssue{m, path, "response " + code + " has no content schema"})
					continue
				}
				if hasContent {
					issues = append(issues, checkMediaSchemas(m, path, "response "+code, content, defined)...)
				}
			}

			// A declared requestBody must carry a content schema.
			if rbAny, ok := op["requestBody"]; ok {
				rb, ok := rbAny.(map[string]any)
				if !ok {
					issues = append(issues, SchemaIssue{m, path, "requestBody is not a mapping"})
					continue
				}
				content, ok := rb["content"].(map[string]any)
				if !ok || len(content) == 0 {
					issues = append(issues, SchemaIssue{m, path, "requestBody has no content schema"})
					continue
				}
				issues = append(issues, checkMediaSchemas(m, path, "requestBody", content, defined)...)
			}
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Path != issues[j].Path {
			return issues[i].Path < issues[j].Path
		}
		if issues[i].Method != issues[j].Method {
			return issues[i].Method < issues[j].Method
		}
		return issues[i].Detail < issues[j].Detail
	})
	return issues
}

// checkMediaSchemas verifies every media type under a content
// object declares a `schema`, and that all $refs within resolve.
func checkMediaSchemas(method, path, where string, content map[string]any, defined map[string]bool) []SchemaIssue {
	var issues []SchemaIssue
	for _, mt := range sortedKeys(content) {
		media, ok := content[mt].(map[string]any)
		if !ok {
			continue
		}
		schema, ok := media["schema"]
		if !ok {
			issues = append(issues, SchemaIssue{method, path, where + " (" + mt + ") has no schema"})
			continue
		}
		for _, ref := range collectRefs(schema) {
			if !defined[ref] {
				issues = append(issues, SchemaIssue{method, path, where + " references undefined schema " + ref})
			}
		}
	}
	return issues
}

// collectRefs walks an arbitrary decoded YAML/JSON node and returns
// every `$ref` string value found (at any depth).
func collectRefs(node any) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		for k, val := range v {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					out = append(out, s)
				}
				continue
			}
			out = append(out, collectRefs(val)...)
		}
	case []any:
		for _, item := range v {
			out = append(out, collectRefs(item)...)
		}
	}
	return out
}

// is2xxWithBody reports whether an OpenAPI response status code
// denotes a success that should carry a body (and therefore a
// schema). 204/205 are bodyless successes.
func is2xxWithBody(code string) bool {
	return len(code) == 3 && code[0] == '2' && code != "204" && code != "205"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
