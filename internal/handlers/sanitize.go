package handlers

import (
	"html"
	"regexp"
	"strings"
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

var (
	hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)
	fontNameRe = regexp.MustCompile(`^[a-zA-Z0-9 _-]{0,40}$`)
)

// SanitizeColor returns input if it's a valid CSS hex color, else "".
// Used to gate values that flow into style="" attributes.
func SanitizeColor(input string) string {
	input = strings.TrimSpace(input)
	if hexColorRe.MatchString(input) {
		return input
	}
	return ""
}

// SanitizeFontName returns input if it's a safe font-family name, else "".
// Allows letters, digits, spaces, underscore, hyphen — sufficient for any
// real font name, blocks the quotes/brackets needed to break out of style="".
func SanitizeFontName(input string) string {
	input = strings.TrimSpace(input)
	if fontNameRe.MatchString(input) {
		return input
	}
	return ""
}

// SanitizeWebURL returns input if it's an http(s) or mailto URL, else "".
// Used for any field that flows into href="" or src="" — blocks the
// javascript: / data: / vbscript: schemes that would execute on click.
func SanitizeWebURL(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	lower := strings.ToLower(input)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") {
		return input
	}
	return ""
}
