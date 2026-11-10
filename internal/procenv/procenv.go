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
	v, _ := e.Lookup(name)
	return v
}

// Lookup is name's value and whether it is set at all, an empty value being set: os.LookupEnv's
// answer when e is nil. A name is matched as the OS matches it (case-blind on Windows: sameName).
func (e Env) Lookup(name string) (string, bool) {
	if e == nil {
		return os.LookupEnv(name)
	}
	v, set := "", false
	for _, kv := range e {
		if k, val, ok := strings.Cut(kv, "="); ok && sameName(k, name) {
			v, set = val, true
		}
	}
	return v, set
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
		out = slices.DeleteFunc(out, func(v string) bool { k, _, _ := strings.Cut(v, "="); return sameName(k, name) })
		if hasValue {
			out = append(out, kv)
		}
	}
	return out
}

// LookPath finds the program name as exec.LookPath does, reading e's variables instead of the
// process's (and is exec.LookPath, for e nil): PATH, and on Windows PATHEXT and
// NoDefaultCurrentDirectoryInExePath. A match found through a relative PATH entry, or Windows'
// implicit current folder, is returned with exec.ErrDot, as exec refuses it. How a name holding a
// path is taken, and how a folder holds a program, is the OS's: lookPath.
func LookPath(e Env, name string) (string, error) {
	if e == nil {
		return exec.LookPath(name)
	}
	switch name {
	case "", ".", "..": // exec.LookPath refuses these on every OS before it looks
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return lookPath(e, name)
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

// ProgramName is the name of the program file is, as the OS names a program: its base name, and on
// Windows that name lower-cased, less an extension e's PATHEXT names (claude.cmd and CLAUDE.EXE are
// claude there). A crew kind, or a wrapper such as env or mise, is matched by it.
func ProgramName(e Env, file string) string { return programName(e, filepath.Base(file)) }
