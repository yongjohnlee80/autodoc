package usershell

import (
	"path/filepath"
	"strings"
)

// LooksUp is the program a command line names, when s looks it up as it is written: a bare name,
// which s finds on PATH, or an absolute path. ok is false when s resolves the program itself, so
// nobody but s can say whether it is there: a line in s's own syntax (an assignment first, a pipe,
// a variable, quoting), a leading ~ (from the home folder), a relative path (from the folder the
// line runs in), a path on a drive but not from its root, a glob.
func (s Shell) LooksUp(line string) (program string, ok bool) {
	if strings.ContainsAny(line, s.syntax()) {
		return "", false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 || strings.Contains(fields[0], "=") {
		return "", false
	}
	word := fields[0]
	switch {
	case filepath.IsAbs(word):
		return word, true
	case filepath.VolumeName(word) != "", strings.ContainsAny(word, "~*?[/"), strings.ContainsRune(word, filepath.Separator):
		return "", false
	}
	return word, true
}

// syntax is the characters that make a line s's own syntax rather than a program and its words.
// PowerShell's has no backslash, a separator on Windows; it has braces (a script block) and @
// (splatting), which /bin/sh's has no need of.
func (s Shell) syntax() string {
	switch s.kind {
	case powershell, winps:
		return ";&|$`()<>'\"{}@"
	default:
		return ";&|$`()<>\\'\""
	}
}

// Describe is how s is said to run a line, for a person: "/bin/sh -c", "C:\...\pwsh.exe -NoProfile
// -Command".
func (s Shell) Describe() string {
	program, args := s.Run("")
	return strings.Join(append([]string{program}, args[:len(args)-1]...), " ")
}
