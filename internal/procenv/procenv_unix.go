//go:build linux || darwin

package procenv

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hasSeparator is whether name is a path rather than a program to look for.
func hasSeparator(name string) bool { return strings.Contains(name, "/") }

// lookPath is exec.LookPath's Unix search, over e's PATH: a name holding a separator is taken as it
// is; an empty PATH entry is the current folder.
func lookPath(e Env, name string) (string, error) {
	if hasSeparator(name) {
		if err := runnable(name); err != nil {
			return "", &exec.Error{Name: name, Err: err}
		}
		return name, nil
	}
	for _, dir := range filepath.SplitList(e.Get("PATH")) {
		if dir == "" {
			dir = "."
		}
		path := filepath.Join(dir, name)
		if runnable(path) != nil {
			continue
		}
		if !filepath.IsAbs(path) {
			return path, &exec.Error{Name: name, Err: exec.ErrDot}
		}
		return path, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
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
