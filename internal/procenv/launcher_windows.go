//go:build windows

package procenv

import (
	"fmt"
	"os"
	"strings"
)

// launcher is the /bin/sh script, for Git's shell, and name.cmd for everything else. The batch file
// turns delayed expansion off (a ! is then itself, whatever the registry says), and each word is
// quoted for cmd.exe and then for the program: % doubled (cmd expands it even inside quotes), a run
// of backslashes before the closing quote doubled (a program reads \" as a quote). Inside quotes &,
// |, <, >, ^ and spaces are themselves.
func launcher(name string, argv []string) ([]LauncherFile, error) {
	words := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsAny(a, "\"\r\n\x00") {
			return nil, fmt.Errorf("%w: %q", ErrLauncherArg, a)
		}
		words[i] = quoteBatch(a)
	}
	batch := "@echo off\r\nsetlocal DisableDelayedExpansion\r\n" + strings.Join(words, " ") + " %*\r\n"
	return []LauncherFile{{Name: name, Text: shScript(argv)}, {Name: name + ".cmd", Text: []byte(batch)}}, nil
}

// quoteBatch is s as one quoted word of a batch file's command line (s holds no quote).
func quoteBatch(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	trailing := len(s) - len(strings.TrimRight(s, `\`))
	return `"` + s + strings.Repeat(`\`, trailing) + `"`
}

// pathEntry is target itself: no link, since a symbolic link needs Developer Mode or an
// administrator, and a Windows name can't hold ':', the separator a link stood in for on Unix.
func pathEntry(_, target string) (string, error) {
	if strings.ContainsRune(target, os.PathListSeparator) {
		return "", fmt.Errorf("the PATH folder %s holds %q, which splits a PATH: move it to a path without it", target, string(os.PathListSeparator))
	}
	return target, nil
}
