// Package proglook finds the programs a crew agent's command may run, by the rules of the OS it
// runs on, using the standard library's own lookup (os/exec): an execute bit on Unix, PATHEXT's
// extensions (claude.exe, claude.cmd) on Windows. It belongs to the comms module, which imports
// nothing else of AutoDoc's (docs/decisions/crew.md#three-modules-and-one-protocol), so the module
// keeps no OS rule of its own.
package proglook

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// All is every program named name on path (a PATH value), in path's order and each once: what a
// lookup would find were each earlier one not there. A relative folder on path, or an empty one, is
// taken from dir, so each answer is an absolute path when dir is. A name that is a path (IsPath)
// is not looked for: none.
func All(name, path, dir string) []string {
	if name == "" || IsPath(name) {
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

// IsPath is whether name names a file by its path (it holds a separator, or on Windows a drive)
// rather than a program to look for on PATH.
func IsPath(name string) bool {
	return filepath.Base(name) != name || filepath.VolumeName(name) != ""
}

// Name is the program a file is: its base name, less the extension that makes it one on Windows
// (mise.exe and mise are both mise).
func Name(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
