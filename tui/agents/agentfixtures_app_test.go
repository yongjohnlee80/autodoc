package agents

import (
	"os"
	"path/filepath"
)

// The agents' cells' fixtures app_test.go held, which move with the agents.

// taskFile is a slash path from a root (/kb/.todo-list/open/t1.md) as an absolute path on this OS:
// itself on Unix; on Windows from the temporary folder's drive (C:\kb\.todo-list\open\t1.md), as a
// task's file is, where /kb/... is not absolute.
func taskFile(p string) string {
	return filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), filepath.FromSlash(p))
}
