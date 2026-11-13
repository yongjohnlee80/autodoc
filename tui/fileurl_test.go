package tui

import (
	"net/url"
	"path/filepath"
	"strings"
)

// fileURL is the file: URL of the absolute path p as a page writes it: its path slash-separated
// with a leading slash, file:///home/me/x.md, and on Windows file:///C:/Users/me/x.md.
func fileURL(p string) string {
	slashed := filepath.ToSlash(p)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}
