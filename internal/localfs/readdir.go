package localfs

import (
	"errors"
	"io/fs"
	"os"
)

// ErrNotAFolder is ReadDirIfAny's answer for a path that holds something other than a folder.
var ErrNotAFolder = errors.New("not a folder")

// ReadDirIfAny reads the folder dir that may not be there, the user's own (a Lua folder, a themes
// folder): nothing at dir is no entries and no error; a file, or anything else that is not a
// folder, is a *fs.PathError wrapping ErrNotAFolder; any other failure is the read's own error.
// A failed read is checked against what is at dir, because the OSes don't agree on what reading a
// file as a folder reports: on Unix it is ENOTDIR, and on Windows (as observed with Go 1.26.11) an
// error errors.Is takes for fs.ErrNotExist, which a reader would take for "no folder". Keep the
// Stat: without it, a file where a folder belongs is silently nothing on Windows.
func ReadDirIfAny(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err == nil {
		return entries, nil
	}
	switch fi, serr := os.Stat(dir); {
	case serr != nil && errors.Is(serr, fs.ErrNotExist):
		return nil, nil // nothing there
	case serr == nil && !fi.IsDir():
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: ErrNotAFolder}
	}
	return nil, err // a folder that can't be read, or a path that can't be looked at: the read's own error
}
