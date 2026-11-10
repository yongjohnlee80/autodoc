//go:build linux || darwin

package procenv

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

// launcher is the /bin/sh script alone.
func launcher(name string, argv []string) ([]LauncherFile, error) {
	return []LauncherFile{{Name: name, Text: shScript(argv)}}, nil
}

// pathEntry is always link: an agent's folder holds its window's scope in its name (autodoc:<id>),
// which a PATH can't hold.
func pathEntry(link, _ string) string { return link }

// makePathEntry makes link a symbolic link to target through a temporary one renamed over it.
func makePathEntry(link, target string) error {
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return err
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	tmp := link + ".new-" + hex.EncodeToString(b)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
