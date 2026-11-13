//go:build windows

package userdirs

import (
	"errors"
	"strings"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"golang.org/x/sys/windows"
)

// HomeVariable is the environment variable that names the user's home folder: %USERPROFILE%, as
// os.UserHomeDir reads it on Windows.
const HomeVariable = "USERPROFILE"

// home is %USERPROFILE%, as os.UserHomeDir reads it on Windows.
func home(env procenv.Env) (string, error) {
	if v := env.Get(HomeVariable); v != "" {
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

// state is %LocalAppData%, where Windows keeps a user's per-machine program data, as ~/.local/state
// is on Unix.
func state(env procenv.Env) (string, error) { return cache(env) }

// temp is the folder GetTempPath names: the first of %TMP%, %TEMP% and %USERPROFILE% that is set at
// all, even set empty (GetTempPath then names no folder), else the Windows folder; made a full path
// as GetTempPath makes it (GetFullPathName, against this process's current folder), with no trailing
// separator, as os.TempDir gives it. GetTempPath2's own folder for a SYSTEM process is not followed.
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
	if dir == "" {
		return ""
	}
	if full, err := fullPath(dir); err == nil {
		dir = full
	}
	if len(dir) == 3 && dir[1] == ':' && dir[2] == '\\' { // a drive's root (C:\) keeps its separator
		return dir
	}
	return strings.TrimSuffix(dir, `\`)
}

// fullPath is path made full by GetFullPathName: a relative one joined to the current folder (a
// drive's own, for C:rel), \rooted on the current drive, / made \, . and .. resolved.
func fullPath(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFullPathName(p, uint32(len(buf)), &buf[0], nil)
		if err != nil {
			return "", err
		}
		if n <= uint32(len(buf)) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
}
