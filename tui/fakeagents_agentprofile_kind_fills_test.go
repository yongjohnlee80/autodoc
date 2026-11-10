package tui

import "strings"

// Copies of the agents' test fakes agentprofile_kind_fills_test.go declares, for the host's cells.

// formAt is the screen cell just inside label's first occurrence, for a click.
func formAtCopy(sc, label string) (int, int) {
	for y, line := range strings.Split(sc, "\n") {
		if before, _, ok := strings.Cut(line, label); ok {
			return len([]rune(before)) + 2, y
		}
	}
	return -1, -1
}
