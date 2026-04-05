package handlers

import (
	"html"
	"regexp"
	"strings"
)

var (
	// Allowed HTML tags for developer "about" fields
	allowedTags    = map[string]bool{"p": true, "br": true, "b": true, "i": true, "em": true, "strong": true, "a": true, "ul": true, "ol": true, "li": true, "h1": true, "h2": true, "h3": true}
	tagPattern     = regexp.MustCompile(`</?([a-zA-Z][a-zA-Z0-9]*)\b[^>]*>`)
	eventPattern   = regexp.MustCompile(`(?i)\s+on\w+\s*=`)
	scriptPattern  = regexp.MustCompile(`(?i)<script[\s>]`)
	hrefJSPattern  = regexp.MustCompile(`(?i)href\s*=\s*["']?\s*javascript:`)
)

// SanitizeHTML strips dangerous tags/attributes, keeping only safe formatting tags.
func SanitizeHTML(input string) string {
	// Remove script tags entirely
	if scriptPattern.MatchString(input) {
		input = regexp.MustCompile(`(?i)<script[^>]*>[\s\S]*?</script>`).ReplaceAllString(input, "")
	}
	// Remove event handlers (onclick, onerror, etc.)
	input = eventPattern.ReplaceAllString(input, " ")
	// Remove javascript: hrefs
	input = hrefJSPattern.ReplaceAllString(input, `href="`)

	// Strip disallowed tags
	input = tagPattern.ReplaceAllStringFunc(input, func(tag string) string {
		matches := tagPattern.FindStringSubmatch(tag)
		if len(matches) < 2 {
			return html.EscapeString(tag)
		}
		tagName := strings.ToLower(matches[1])
		if allowedTags[tagName] {
			return tag
		}
		return html.EscapeString(tag)
	})

	return input
}

// SanitizePlain escapes all HTML — for notifications, review text, usernames, etc.
func SanitizePlain(input string) string {
	return html.EscapeString(input)
}
