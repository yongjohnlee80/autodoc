package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/golib/errs"
)

// Under reports whether p is dir or inside it, by whole segments: "a/bc" is not under "a/b". Every
// relative path is under ".".
func Under(p, dir string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

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
