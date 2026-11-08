package app

import "testing"

// TestTheTerminalReadsTheClipboard: AutoDoc's terminal opens with the platform's options and two
// more, the clipboard read through the terminal (OSC 52), on by default, and plain frame drags;
// golib's own cells prove what each does, and tui's TestAPlainDragMovesAndResizesAPanel AutoDoc's
// panels on such a terminal.
func TestTheTerminalReadsTheClipboard(t *testing.T) {
	if got, platform := len(terminalOptions()), len(termOptions()); got != platform+2 {
		t.Fatalf("terminalOptions() = %d options, want the platform's %d, the clipboard read and plain frame drags", got, platform)
	}
}
