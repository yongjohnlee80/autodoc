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

// temp is the folder GetTempPath names: the first of %TMP%, %TEMP% and %USERPROFILE% that is set at
// all, even set empty (GetTempPath then names no folder), else the Windows folder; with no trailing
// separator, as os.TempDir gives it.
func temp(env procenv.Env) string {
	dir, set := "", false
	for _, name := range []string{"TMP", "TEMP", "USERPROFILE"} {
		if dir, set = env.Lookup(name); set {
			break
		}
	}
	if !set {
		if dir = env.Get("SystemRoot"); dir == "" {
			dir = `C:\Windows`
		}
	}
	if len(dir) > 3 { // a drive's root (C:\) keeps its separator
		dir = strings.TrimRight(dir, `\`)
	}
	return dir
}
