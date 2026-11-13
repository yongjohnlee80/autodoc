package procenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// An agent's own autodoc is first on its PATH, so a bare autodoc in its shell is the window's build
// on the window's config. How is the OS's (launcher_*.go): on Unix a folder of the agent's own,
// reached through a link, holding a /bin/sh script that runs the build on the config; on Windows
// the build's own folder, the config reaching it through $AUTODOC_CONFIG (no script a Windows
// program could run carries its caller's arguments as themselves).

// LauncherFile is one file of an agent's launcher folder: its name there and its text, to be written
// so its owner may run it (0700 where modes mean anything).
type LauncherFile struct {
	Name string
	Text []byte
}

// Launcher is the files that make name, in the agent's launcher folder, run argv with the caller's
// own arguments after it: on Unix a /bin/sh script; on Windows none, the build being on the PATH
// itself. argv's first word is the program, an absolute path.
func Launcher(name string, argv []string) ([]LauncherFile, error) {
	if len(argv) == 0 {
		return nil, errors.New("a launcher needs a program")
	}
	return launcher(name, argv)
}

// PathEntry is the folder to put first on an agent's PATH: on Unix link, a symbolic link to target
// (the agent's launcher folder, whose own name may hold ':') that MakePathEntry makes; on Windows
// program's own folder, program being the build the agent runs (Self, or a test's).
func PathEntry(link, target, program string) string { return pathEntry(link, target, program) }

// MakePathEntry makes PathEntry(link, target, program) lead where it should: on Unix the link to
// target, replacing one that points elsewhere or nowhere; on Windows nothing. An entry that would
// hold the PATH separator itself is refused, naming it: a PATH split there finds the programs of
// some other folder.
func MakePathEntry(link, target, program string) error {
	entry := pathEntry(link, target, program)
	if strings.ContainsRune(entry, os.PathListSeparator) {
		return fmt.Errorf("the PATH folder %s holds %q, which splits a PATH: move it to a path without it", entry, string(os.PathListSeparator))
	}
	return makePathEntry(link, target)
}

// Self is the program running, as the OS names it (os.Executable), with any link it was started
// through followed: the build an agent's PATH leads to.
func Self() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// quotePOSIX is s as one word of a /bin/sh line, standing for itself.
func quotePOSIX(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// shScript is a /bin/sh script that runs argv, then its own arguments.
func shScript(argv []string) []byte {
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = quotePOSIX(a)
	}
	return []byte("#!/bin/sh\nexec " + strings.Join(words, " ") + ` "$@"` + "\n")
}
