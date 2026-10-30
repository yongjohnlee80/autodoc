package schema

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath is where a workspace's schema path p names its file: "~/" is the home folder, a
// relative path is under root. The daemon's schema holder loads the file it names, and a gated
// script's schema path is checked as this names it, so the file checked is the file loaded.
func ResolvePath(root, p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}
