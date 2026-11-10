//go:build linux || darwin

package procenv

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// launcher is the /bin/sh script alone.
func launcher(name string, argv []string) ([]LauncherFile, error) {
	return []LauncherFile{{Name: name, Text: shScript(argv)}}, nil
}

// pathEntry is always link: an agent's folder holds its window's scope in its name (autodoc:<id>),
// which a PATH can't hold. The link is made through a temporary one renamed over it.
func pathEntry(link, target string) (string, error) {
	if strings.ContainsRune(link, os.PathListSeparator) {
		return "", fmt.Errorf("the PATH folder %s holds %q, which splits a PATH: move it to a path without it", link, string(os.PathListSeparator))
	}
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return link, nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return "", err
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	tmp := link + ".new-" + hex.EncodeToString(b)
	if err := os.Symlink(target, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return link, nil
}
