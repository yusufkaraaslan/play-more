package handlers

import (
	"html"
)

// SanitizePlain escapes all HTML — use for stored fields that will be
// rendered as text (bio, notifications, review text, etc).
//
// Policy: PlayMore stores user-supplied text *raw* and escapes it at render time
// in the SPA via escapeHtml(). This function provides a server-side defense layer
// for fields that should never contain HTML (bio, devlog content, etc).
//
// We deliberately do NOT provide a "safe HTML" allowlist sanitizer — regex-based
// HTML allowlists are routinely bypassed (e.g. <a/onclick=...>, malformed tags,
// HTML entities in attributes). If you need rich text in a future feature, use
// a dedicated library like github.com/microcosm-cc/bluemonday.
func SanitizePlain(input string) string {
	return html.EscapeString(input)
}
