package crew

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// Finding a crew agent's program follows the OS's rules through the standard library's own lookup
// (os/exec.LookPath on each PATH folder's candidate: PATHEXT's claude.exe and claude.cmd on Windows,
// an execute bit on Unix), so the comms module keeps no OS rule of its own, and imports nothing else of
// AutoDoc's (docs/decisions/crew.md#three-modules-and-one-protocol). It needs every program on PATH,
// not only the first (exec.LookPath's), since a wrapper found first is tried past.

// programsOn is every program named name on path (a PATH value), in path's order and each once: what a
// lookup would find were each earlier one not there. A relative folder on path, or an empty one, is
// taken from dir, so each answer is an absolute path when dir is. A name that is a path (isPath)
// is not looked for: none.
func programsOn(name, path, dir string) []string {
	if name == "" || isPath(name) {
		return nil
	}
	var out []string
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			d = "."
		}
		if !filepath.IsAbs(d) {
			d = filepath.Join(dir, d)
		}
		f, err := exec.LookPath(filepath.Join(d, name))
		if err != nil || slices.Contains(out, f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// isPath is whether name names a file by its path (it holds a separator, or on Windows a drive)
// rather than a program to look for on PATH.
func isPath(name string) bool {
	return filepath.Base(name) != name || filepath.VolumeName(name) != ""
}

// isProgram is whether the file at path is the program name by the OS's rules: what the standard
// library's lookup finds for name in path's folder is that very file. On Unix it is the name alone
// (mise.sh is not mise); on Windows, the name with an extension PATHEXT names, in any case (mise.exe,
// MISE.EXE). The comms module keeps no extension rule of its own.
func isProgram(path, name string) bool {
	f, err := exec.LookPath(filepath.Join(filepath.Dir(path), name))
	if err != nil {
		return false
	}
	a, errA := os.Stat(f)
	b, errB := os.Stat(path)
	return errA == nil && errB == nil && os.SameFile(a, b)
}
