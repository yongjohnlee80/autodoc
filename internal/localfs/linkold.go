package localfs

import (
	"fmt"
	"os"
	"path/filepath"
)

// windows is whether this OS follows Windows' rule, decided as core/paths decides it: by the
// separator.
var windows = os.PathSeparator == '\\'

// LinkOldName leaves, at oldPath, a link to the folder just renamed from it to newPath beside it,
// so a program still holding oldPath (an agent of a window started before the rename, whose
// environment names its folder by that path) reaches the folder by it: a relative symbolic link on
// Linux and macOS, which moves with the folder holding both. On Windows it does nothing: Windows
// refuses to rename a folder in use, so a folder renamed there is one nothing held.
func LinkOldName(oldPath, newPath string) error {
	return linkOldName(oldPath, newPath, windows)
}

func linkOldName(oldPath, newPath string, windows bool) error {
	if windows {
		return nil
	}
	if filepath.Dir(oldPath) != filepath.Dir(newPath) {
		return fmt.Errorf("localfs: a link from %s to %s: not in one folder", oldPath, newPath)
	}
	if err := os.Symlink(filepath.Base(newPath), oldPath); err != nil {
		return fmt.Errorf("localfs: a link at %s to the renamed %s: %w", oldPath, filepath.Base(newPath), err)
	}
	return nil
}
