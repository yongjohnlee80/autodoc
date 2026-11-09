//go:build windows

package procenv

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// hasSeparator is whether name is a path rather than a program to look for: it holds a separator
// or a drive's colon.
func hasSeparator(name string) bool { return strings.ContainsAny(name, `\/:`) }

// pathExts are the extensions a program is found with, from the Env's PATHEXT (Windows' default
// when it has none), lowercased.
func pathExts(e Env) []string {
	x := e.Get("PATHEXT")
	if x == "" {
		x = ".COM;.EXE;.BAT;.CMD"
	}
	var out []string
	for _, ext := range strings.Split(x, ";") {
		if ext = strings.ToLower(strings.TrimSpace(ext)); ext != "" {
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			out = append(out, ext)
		}
	}
	return out
}

// lookIn is dir's program name, as exec.LookPath finds it on Windows: name itself when its
// extension is one of PATHEXT's, else name with each of PATHEXT's in turn.
func lookIn(e Env, dir, name string) (string, bool) {
	exts := pathExts(e)
	base := filepath.Join(dir, name)
	for _, ext := range exts {
		if strings.EqualFold(filepath.Ext(name), ext) && runnable(base) == nil {
			return base, true
		}
	}
	for _, ext := range exts {
		if path := base + ext; runnable(path) == nil {
			return path, true
		}
	}
	return "", false
}

// runnable is nil when path is a file, not a folder: Windows runs a file by its extension, which
// lookIn has already matched against PATHEXT.
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
