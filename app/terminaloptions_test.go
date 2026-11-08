package app

import "testing"

// TestTheTerminalReadsTheClipboard: AutoDoc's terminal opens with the platform's options and one
// more, the clipboard read through the terminal (OSC 52), on by default; golib's own cells prove
// what it does.
func TestTheTerminalReadsTheClipboard(t *testing.T) {
	if got, platform := len(terminalOptions()), len(termOptions()); got != platform+1 {
		t.Fatalf("terminalOptions() = %d options, want the platform's %d and the clipboard read", got, platform)
	}
}
