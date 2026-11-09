//go:build windows

package userdirs

import (
	"errors"
	"strings"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// home is %USERPROFILE%, as os.UserHomeDir reads it on Windows.
func home(env procenv.Env) (string, error) {
	if v := env.Get("USERPROFILE"); v != "" {
		return v, nil
	}
	return "", errors.New("%userprofile% is not defined")
}

// cache is %LocalAppData%, as os.UserCacheDir reads it on Windows.
func cache(env procenv.Env) (string, error) {
	if v := env.Get("LocalAppData"); v != "" {
		return v, nil
	}
	return "", errors.New("%LocalAppData% is not defined")
}

// temp is the folder GetTempPath names, in its order: %TMP%, %TEMP%, %USERPROFILE%, then the
// Windows folder; with no trailing separator, as os.TempDir gives it.
func temp(env procenv.Env) string {
	dir := ""
	for _, name := range []string{"TMP", "TEMP", "USERPROFILE", "SystemRoot"} {
		if dir = env.Get(name); dir != "" {
			break
		}
	}
	if dir == "" {
		dir = `C:\Windows`
	}
	if len(dir) > 3 { // a drive's root (C:\) keeps its separator
		dir = strings.TrimRight(dir, `\`)
	}
	return dir
}
