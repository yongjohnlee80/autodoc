//go:build linux || darwin

package usershell

import (
	"slices"
	"testing"
)

// On Unix a command line's words are /bin/sh's.
func TestTheCommandWordsArePOSIXOnUnix(t *testing.T) {
	line := `/opt/a\ b/claude --model "haiku 4"`
	if got, want := texts(Words(line)), texts(POSIXWords(line)); !slices.Equal(got, want) {
		t.Errorf("Words(%q) = %q, want POSIX's %q", line, got, want)
	}
}
