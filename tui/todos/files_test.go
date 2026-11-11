package todos

import (
	"os"
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/core/paths"
)

// Copies of the host's test fakes (fakeagents_agentfixtures_app_test.go, app_test.go) the todos'
// cells read, and a preferences view of their own.

// taskFile is a slash path from a root (/kb/.todo-list/open/t1.md) as an absolute path on this OS:
// itself on Unix; on Windows from the temporary folder's drive (C:\kb\.todo-list\open\t1.md), as a
// task's file is, where /kb/... is not absolute.
func taskFile(p string) string {
	return filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), filepath.FromSlash(p))
}

// slashFile is taskFile's path in the slash form, the daemon's for a store or a root, its volume
// kept: itself on Unix, C:/kb/.todo-list on Windows.
func slashFile(p string) string { return paths.Slash(taskFile(p)) }

// edgePrefs is a preferences view answering the values it holds, as the host's preferences do for
// the todos panel's edge (feature.PrefsView); no panel has a geometry in it.
type edgePrefs map[string]string

func (p edgePrefs) Value(name string) string              { return p[name] }
func (edgePrefs) PanelGeometry(string, string) (int, int) { return 0, 0 }
