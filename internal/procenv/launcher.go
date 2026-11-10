package procenv

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// A LAUNCHER is a folder put first on a PATH, holding a program by name that runs another with
// arguments of its own first: an agent's autodoc, which runs the window's build on its config.
// Where the folder is and which files make the program are the OS's (launcher_*.go).

// LauncherFile is one file a launcher is made of: its name in the launcher's folder and its text,
// to be written so its owner may run it (0700 where modes mean anything).
type LauncherFile struct {
	Name string
	Text []byte
}

// ErrLauncherArg is an argument a launcher can't carry as itself: on Windows one holding a double
// quote or a line break, which cmd.exe's reading of a batch file would split or end (no Windows path
// holds either).
var ErrLauncherArg = errors.New("an argument a launcher can't carry as itself")

// Launcher is the files that make name, run from a launcher's folder, run argv with the caller's own
// arguments after it: a /bin/sh script on Unix; on Windows the same script, which Git's shell runs,
// and name.cmd, which cmd.exe, PowerShell and a PATHEXT lookup find. argv's first word is the
// program, an absolute path.
func Launcher(name string, argv []string) ([]LauncherFile, error) {
	if len(argv) == 0 {
		return nil, errors.New("a launcher needs a program")
	}
	return launcher(name, argv)
}

// PathEntry is the folder to put first on a PATH for the programs in target: target itself where
// its path can sit on a PATH (Windows), else link (Unix, where a folder's name may hold ':'), a
// symbolic link to target that MakePathEntry makes.
func PathEntry(link, target string) string { return pathEntry(link, target) }

// MakePathEntry makes PathEntry(link, target) lead to target: on Unix the link, replacing one that
// points elsewhere or nowhere; on Windows nothing. An entry that would hold the PATH separator
// itself is refused, naming it: a PATH split there finds the programs of some other folder.
func MakePathEntry(link, target string) error {
	entry := pathEntry(link, target)
	if strings.ContainsRune(entry, os.PathListSeparator) {
		return fmt.Errorf("the PATH folder %s holds %q, which splits a PATH: move it to a path without it", entry, string(os.PathListSeparator))
	}
	return makePathEntry(link, target)
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
