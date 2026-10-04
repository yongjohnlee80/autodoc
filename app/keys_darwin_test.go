package app

import "testing"

// TestTheMacFoldsOption: on macOS the terminal is opened with one option, the Option fold (golib's
// own tests prove what it does to the keys).
func TestTheMacFoldsOption(t *testing.T) {
	if opts := termOptions(); len(opts) != 1 {
		t.Fatalf("termOptions() = %d options on macOS, want the Option fold", len(opts))
	}
}
