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

	// CSS sanitisation — block data-exfiltration and XSS vectors.
	cssImportRe      = regexp.MustCompile(`(?i)@import\b[^;]*;?`)
	cssFontFaceRe    = regexp.MustCompile(`(?i)@font-face\b[^{]*\{[^}]*\}`)
	cssExternalURLRe = regexp.MustCompile(`(?i)url\s*\(\s*['"]?\s*https?://[^)]+['"]?\s*\)`)
	cssExpressionRe  = regexp.MustCompile(`(?i)expression\s*\(`)
	cssBehaviorRe    = regexp.MustCompile(`(?i)behavior\s*:`)
	cssMozBindingRe  = regexp.MustCompile(`(?i)-moz-binding\s*:`)
	cssJSURLRe       = regexp.MustCompile(`(?i)javascript\s*:`)
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

// SanitizeCSS sanitises user-supplied custom CSS for developer pages.
// It strips patterns that enable data exfiltration or XSS:
//   • @import / @font-face  (external resource loading)
//   • url(https://…)         (external URLs in backgrounds, cursors, etc.)
//   • expression(…)          (IE legacy XSS)
//   • behavior: / -moz-binding: (legacy XSS)
//   • javascript:            (scheme-based XSS)
//   • < >                    (</style> injection)
//
// This is a *server-side* layer — the frontend also runs its own sanitiser,
// but an attacker could bypass the frontend by calling the API directly.
func SanitizeCSS(css string) string {
	if css == "" {
		return ""
	}

	// Prevent </style> injection
	css = strings.ReplaceAll(css, "<", "")
	css = strings.ReplaceAll(css, ">", "")

	// Block external resource loading via @import and @font-face
	css = cssImportRe.ReplaceAllString(css, "")
	css = cssFontFaceRe.ReplaceAllString(css, "")

	// Block external URLs — only data: and relative/local refs remain safe
	css = cssExternalURLRe.ReplaceAllString(css, "url()")

	// Block IE/legacy XSS vectors
	css = cssExpressionRe.ReplaceAllString(css, "/*expr*/(")
	css = cssBehaviorRe.ReplaceAllString(css, "/*beh*/:")
	css = cssMozBindingRe.ReplaceAllString(css, "/*moz*/:")
	css = cssJSURLRe.ReplaceAllString(css, "/*js*/:")

	// Cap length
	if len(css) > 5000 {
		css = css[:5000]
	}

	return css
}
