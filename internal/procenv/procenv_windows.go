//go:build windows

package procenv

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hasSeparator is whether name is a path rather than a program to look for: it holds a separator
// or a drive's colon.
func hasSeparator(name string) bool { return strings.ContainsAny(name, `\/:`) }

// lookPath is exec.LookPath's Windows search (os/exec's lp_windows.go), over e's variables. A name
// holding a path gets PATHEXT's extensions too. A bare name is looked for first in the current
// folder, unless e sets NoDefaultCurrentDirectoryInExePath (to anything, even empty), and that
// match comes with exec.ErrDot unless PATH reaches the same file; an empty PATH entry is skipped.
// exec's GODEBUG execerrdot=0, which drops the ErrDot, is not followed: an Env's lookup always
// reports it.
func lookPath(e Env, name string) (string, error) {
	exts := pathExts(e)
	if hasSeparator(name) {
		path, err := findExecutable(name, exts)
		if err != nil {
			return "", &exec.Error{Name: name, Err: err}
		}
		return path, nil
	}
	var dot string
	var dotErr error
	if _, skip := e.Lookup("NoDefaultCurrentDirectoryInExePath"); !skip {
		if path, err := findExecutable(filepath.Join(".", name), exts); err == nil {
			dot, dotErr = path, &exec.Error{Name: name, Err: exec.ErrDot}
		}
	}
	for _, dir := range filepath.SplitList(e.Get("PATH")) {
		if dir == "" {
			continue
		}
		path, err := findExecutable(filepath.Join(dir, name), exts)
		if err != nil {
			continue
		}
		if dotErr != nil && !sameFile(dot, path) {
			return dot, dotErr // the current folder's comes first, and PATH's is another program
		}
		if !filepath.IsAbs(path) {
			if dotErr == nil {
				dot, dotErr = path, &exec.Error{Name: name, Err: exec.ErrDot}
			}
			continue
		}
		return path, nil
	}
	if dotErr != nil {
		return dot, dotErr
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// pathExts are the extensions a program is found with, from e's PATHEXT (Windows' default when it
// is empty or not set), lowercased, each with its leading dot.
func pathExts(e Env) []string {
	x := e.Get("PATHEXT")
	if x == "" {
		return []string{".com", ".exe", ".bat", ".cmd"}
	}
	var out []string
	for _, ext := range strings.Split(strings.ToLower(x), ";") {
		if ext == "" {
			continue
		}
		if ext[0] != '.' {
			ext = "." + ext
		}
		out = append(out, ext)
	}
	return out
}

// findExecutable is file, or file with one of exts, whichever is a file first: file itself when it
// has an extension of any kind, then each of exts appended (so foo.bat.exe is found for foo.bat).
// No exts at all takes file as it is.
func findExecutable(file string, exts []string) (string, error) {
	if len(exts) == 0 {
		return file, runnable(file)
	}
	if hasExt(file) && runnable(file) == nil {
		return file, nil
	}
	for _, ext := range exts {
		if path := file + ext; runnable(path) == nil {
			return path, nil
		}
	}
	if hasExt(file) {
		return "", fs.ErrNotExist
	}
	return "", exec.ErrNotFound
}

// hasExt is whether file's last element has a dot in it.
func hasExt(file string) bool {
	i := strings.LastIndex(file, ".")
	return i >= 0 && strings.LastIndexAny(file, `:\/`) < i
}

// runnable is nil when path is a file, not a folder: Windows runs a file by its extension.
func runnable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fs.ErrPermission
	}
	return nil
}

// sameFile is whether a and b name one file, as Lstat sees them.
func sameFile(a, b string) bool {
	ai, aErr := os.Lstat(a)
	bi, bErr := os.Lstat(b)
	return aErr == nil && bErr == nil && os.SameFile(ai, bi)
}

// sameName is whether two variable names are one: equal but for case, as Windows compares them.
func sameName(a, b string) bool { return strings.EqualFold(a, b) }
