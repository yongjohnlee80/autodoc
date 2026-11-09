//go:build linux || darwin

package procenv

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// hasSeparator is whether name is a path rather than a program to look for.
func hasSeparator(name string) bool { return strings.Contains(name, "/") }

// lookIn is dir's program name, when it is a file that may be run.
func lookIn(_ Env, dir, name string) (string, bool) {
	path := filepath.Join(dir, name)
	return path, runnable(path) == nil
}

// runnable is nil when path is a file with an execute bit, as exec.LookPath reads it.
func runnable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if m := fi.Mode(); m.IsDir() || m&0o111 == 0 {
		return fs.ErrPermission
	}
	return nil
}

// sameName is whether two variable names are one: exactly equal, as Unix compares them.
func sameName(a, b string) bool { return a == b }
