package localfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// ErrNotAFolder is ReadDirIfAny's answer for a path that holds something other than a folder.
var ErrNotAFolder = errors.New("not a folder")

// ReadDirIfAny reads the folder dir that may not be there, the user's own (a Lua folder, a themes
// folder): nothing at dir is no entries and no error; a file, or anything else that is not a
// folder, is an error that wraps ErrNotAFolder. Every OS answers alike: a failed read is checked
// against what is at dir, since the OSes report reading a file as a folder differently (Unix says
// ENOTDIR; Windows says "not found", FindFirstFile on dir\*, which os.ReadDir passes on as
// fs.ErrNotExist).
func ReadDirIfAny(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err == nil {
		return entries, nil
	}
	switch fi, serr := os.Stat(dir); {
	case serr != nil && errors.Is(serr, fs.ErrNotExist):
		return nil, nil // nothing there
	case serr == nil && !fi.IsDir():
		return nil, fmt.Errorf("%s: %w", dir, ErrNotAFolder)
	}
	return nil, err // a folder that can't be read, or a path that can't be looked at: the read's own error
}
