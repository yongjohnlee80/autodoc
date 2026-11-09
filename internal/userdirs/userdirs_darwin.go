package userdirs

import (
	"errors"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// cache is ~/Library/Caches.
func cache(env procenv.Env) (string, error) {
	h := env.Get("HOME")
	if h == "" {
		return "", errors.New("$HOME is not defined")
	}
	return h + "/Library/Caches", nil
}
