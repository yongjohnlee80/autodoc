//go:build !darwin

package app

import "testing"

// TestNoTerminalOptionsOffTheMac: only macOS folds Option into Alt; elsewhere Alt arrives as Alt.
func TestNoTerminalOptionsOffTheMac(t *testing.T) {
	if opts := termOptions(); len(opts) != 0 {
		t.Fatalf("termOptions() = %d options off macOS, want none", len(opts))
	}
}
