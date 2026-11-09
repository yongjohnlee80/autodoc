//go:build linux || darwin

package userdirs

import (
	"errors"
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

func home(env procenv.Env) (string, error) {
	if v := env.Get("HOME"); v != "" {
		return v, nil
	}
	return "", errors.New("$HOME is not defined")
}

// state is $XDG_STATE_HOME when absolute (the XDG rule: a relative value is ignored), else
// ~/.local/state.
func state(env procenv.Env) (string, error) {
	if d := env.Get("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return d, nil
	}
	h, err := home(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "state"), nil
}

func temp(env procenv.Env) string {
	if dir := env.Get("TMPDIR"); dir != "" {
		return dir
	}
	return "/tmp"
}
