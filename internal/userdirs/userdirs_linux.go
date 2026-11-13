package userdirs

import (
	"errors"
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// cache is $XDG_CACHE_HOME, an absolute path, else ~/.cache.
func cache(env procenv.Env) (string, error) {
	dir := env.Get("XDG_CACHE_HOME")
	if dir == "" {
		h := env.Get("HOME")
		if h == "" {
			return "", errors.New("neither $XDG_CACHE_HOME nor $HOME are defined")
		}
		return h + "/.cache", nil
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("path in $XDG_CACHE_HOME is relative")
	}
	return dir, nil
}
