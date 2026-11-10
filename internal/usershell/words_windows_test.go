package usershell

import (
	"slices"
	"testing"
)

// On Windows a command line's words are PowerShell's: the shell command lines run in there.
func TestTheCommandWordsArePowerShellsOnWindows(t *testing.T) {
	line := `& 'C:\Program Files\x\claude.cmd' --model "haiku 4"`
	if got, want := texts(Words(line)), texts(powerShellWords(line)); !slices.Equal(got, want) {
		t.Errorf("Words(%q) = %q, want PowerShell's %q", line, got, want)
	}
}
