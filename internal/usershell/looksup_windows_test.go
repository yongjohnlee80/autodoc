//go:build windows

package usershell

import "testing"

// On Windows an absolute path, a drive's or a share's, either separator, is looked up as it is
// written; a path on a drive but not from its root, or from the current drive's root, is the shell's
// to resolve.
func TestAnAbsolutePathIsLookedUpOnWindows(t *testing.T) {
	for line, want := range map[string]string{
		`C:\Tools\claude.exe --x`: `C:\Tools\claude.exe`,
		`C:/Tools/claude --x`:     `C:/Tools/claude`,
		`\\host\share\claude`:     `\\host\share\claude`,
		`C:Tools\claude`:          "",
		`\Tools\claude`:           "",
		`Tools\claude`:            "",
	} {
		for _, k := range []kind{powershell, winps} {
			got, ok := (Shell{kind: k}).LooksUp(line)
			if ok != (want != "") || got != want {
				t.Errorf("kind %d, %q: %q %v; want %q", k, line, got, ok, want)
			}
		}
	}
}
