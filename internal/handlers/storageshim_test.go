package handlers

import (
	"strings"
	"testing"
)

// The shim must land in a spot that keeps the browser in standards mode.
// Anything emitted before <!doctype html> triggers quirks mode, where the
// percentage heights that full-viewport canvas games depend on collapse — a
// far more confusing regression than the crash we set out to fix.
func TestShimInsertOffset(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string // the text the offset must sit immediately after
	}{
		{"head", "<!doctype html><html><head><title>x</title>", "<head>"},
		{"head with attrs", "<!DOCTYPE html><html><head data-x=\"1\">", "<head data-x=\"1\">"},
		{"uppercase", "<!DOCTYPE HTML><HTML><HEAD>", "<HEAD>"},
		{"no head", "<!doctype html><html lang=\"en\"><body>", "<html lang=\"en\">"},
		{"doctype only", "<!doctype html><body>hi", "<!doctype html>"},
		{"comment decoy", "<!doctype html><!-- <head> --><html><head>", "<html><head>"},
		{"quoted bracket", "<!doctype html><html><head data-x=\"a>b\">", "<head data-x=\"a>b\">"},
		{"bare fragment", "<canvas></canvas>", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shimInsertOffset([]byte(tc.html))
			if !strings.HasSuffix(tc.html[:got], tc.want) {
				t.Errorf("offset %d puts shim after %q, want it to follow %q", got, tc.html[:got], tc.want)
			}
		})
	}
}

// A tag straddling the end of the scan window has no '>' to anchor on. Taking
// the match anyway would splice the shim into the middle of the tag.
func TestShimInsertOffset_TruncatedTag(t *testing.T) {
	if got := shimInsertOffset([]byte("<!doctype html><html><hea")); got != 21 {
		t.Errorf("truncated <head> must fall back to after <html> (21), got %d", got)
	}
}

// bytes.ToLower folds by rune and would shift offsets on any document
// containing U+0130 and friends; this fold is ASCII-only for that reason.
func TestASCIIIndexFold(t *testing.T) {
	// U+0130 is two bytes; the returned offset must index the ORIGINAL buffer,
	// which is exactly what a bytes.ToLower-based search would get wrong.
	hay := []byte("<!-- İ --><HeAd>")
	got := asciiIndexFold(hay, "<head")
	if got == -1 || !strings.EqualFold(string(hay[got:got+5]), "<head") {
		t.Errorf("offset %d does not point at the tag in the original buffer", got)
	}
	if got := asciiIndexFold([]byte("<body>"), "<head"); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}
