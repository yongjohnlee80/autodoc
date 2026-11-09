//go:build linux || darwin

package userdirs

import (
	"errors"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

func home(env procenv.Env) (string, error) {
	if v := env.Get("HOME"); v != "" {
		return v, nil
	}
	return "", errors.New("$HOME is not defined")
}

func temp(env procenv.Env) string {
	if dir := env.Get("TMPDIR"); dir != "" {
		return dir
	}
	return "/tmp"
}
