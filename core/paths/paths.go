package paths

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/golib/errs"
	ospaths "github.com/yongjohnlee80/golib/paths"
)

// Under reports whether p is dir or inside it, by whole segments: "a/bc" is not under "a/b". Every
// relative path is under ".".
func Under(p, dir string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

// Slash is the OS path p in the form the core keeps paths in: slash-separated and clean, so a
// Windows path `C:\Users\u` is "C:/Users/u". On a system whose separator is "/" it is p cleaned. ""
// stays "": no path is not the current directory.
func Slash(p string) string {
	if p == "" {
		return ""
	}
	return path.Clean(filepath.ToSlash(p))
}

// IsAbs reports whether p, in either form (the OS's own or Slash's), is an absolute path: rooted
// ("/srv/kb"), or absolute on this OS ("C:/Users/u" and `C:\Users\u` on Windows; "C:u", a drive's
// current directory, is not). The disk that opens p has the last word: a rooted path with no drive
// is refused there on Windows.
func IsAbs(p string) bool {
	return strings.HasPrefix(p, "/") || ospaths.IsAbs(ospaths.OS(filepath.FromSlash(p)))
}

// ErrNoHome is a "~" path expanded with no home directory to put in its place.
var ErrNoHome = errs.Sentinel(errs.ErrInvalidArgument, "paths: no home directory")

// ExpandHome is p with a leading "~" or "~/" made the user's home directory. Any other path is
// returned as it is.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: %q needs one", ErrNoHome, p)
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}
