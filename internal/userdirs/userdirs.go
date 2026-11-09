// Package userdirs is where the user's folders are: the home, the cache and the folder for
// temporary files, each read from an environment (procenv.Env). For the process's own (nil) they
// are os.UserHomeDir, os.UserCacheDir and os.TempDir themselves; for one of a caller's own, they
// are found the way those calls find them on this OS, from its variables alone.
//
// The OS-specific part, which variables name which folder, is in this package's per-OS files.
//
// Concurrency: the functions only read their Env; call them from anywhere.
package userdirs

import (
	"os"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// Home is the user's home folder, as os.UserHomeDir finds it.
func Home(env procenv.Env) (string, error) {
	if env == nil {
		return os.UserHomeDir()
	}
	return home(env)
}

// Cache is the user's cache folder, as os.UserCacheDir finds it.
func Cache(env procenv.Env) (string, error) {
	if env == nil {
		return os.UserCacheDir()
	}
	return cache(env)
}

// Temp is the folder for temporary files, as os.TempDir finds it.
func Temp(env procenv.Env) string {
	if env == nil {
		return os.TempDir()
	}
	return temp(env)
}
