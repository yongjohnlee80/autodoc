// Package procenv is a process's environment, as a value: its variables, the environment a program
// it starts inherits, and the programs found on its PATH. Env nil is the process's own, read
// through os and os/exec themselves, so a caller holding none behaves exactly as those calls do;
// an Env of its own (a test's) is read the same way on every OS, from its variables alone.
//
// The OS-specific part, how a PATH entry holds a program, is in this package's per-OS files.
//
// Concurrency: an Env is a value that no function here changes; share it freely.
package procenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Env is an environment in os.Environ's form, NAME=value, a later setting of a name winning, as it
// does for a process. nil is the process's own; Env{} is an empty one.
type Env []string

// Get is name's value, "" when it has none.
func (e Env) Get(name string) string {
	if e == nil {
		return os.Getenv(name)
	}
	v := ""
	for _, kv := range e {
		if k, val, ok := strings.Cut(kv, "="); ok && k == name {
			v = val
		}
	}
	return v
}

// Environ is the environment, os.Environ's when e is nil: what a program started with it inherits.
// The result is the caller's own to change.
func (e Env) Environ() []string {
	if e == nil {
		return os.Environ()
	}
	return slices.Clone(e)
}

// With is e with each of set applied in turn: NAME=value sets NAME, NAME alone removes it. With
// on nil starts from the process's environment, and the result is an Env of its own.
func (e Env) With(set ...string) Env {
	out := Env(e.Environ())
	for _, kv := range set {
		name, _, hasValue := strings.Cut(kv, "=")
		out = slices.DeleteFunc(out, func(v string) bool { k, _, _ := strings.Cut(v, "="); return k == name })
		if hasValue {
			out = append(out, kv)
		}
	}
	return out
}

// LookPath finds the program name on e's PATH, as exec.LookPath does on the process's (which it
// is, for e nil): a name holding a path separator is taken as it is; a match found through a
// relative PATH entry is returned with exec.ErrDot, as exec refuses it.
func LookPath(e Env, name string) (string, error) {
	if e == nil {
		return exec.LookPath(name)
	}
	if hasSeparator(name) {
		if err := runnable(name); err != nil {
			return "", &exec.Error{Name: name, Err: err}
		}
		return name, nil
	}
	for _, dir := range filepath.SplitList(e.Get("PATH")) {
		if dir == "" {
			dir = "." // an empty entry is the current folder
		}
		if path, ok := lookIn(e, dir, name); ok {
			if !filepath.IsAbs(path) {
				return path, &exec.Error{Name: name, Err: exec.ErrDot}
			}
			return path, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// Resolve is name found on e's PATH, for exec.Command, which finds a bare name on the process's
// PATH: name itself when e is nil, holds a separator, or is not found (Start then reports it).
func Resolve(e Env, name string) string {
	if e == nil || hasSeparator(name) {
		return name
	}
	if path, err := LookPath(e, name); err == nil {
		return path
	}
	return name
}
