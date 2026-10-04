package tui

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTheLeaderCardsColumnsAlign: every row's second command starts in the same column, so its
// key letters line up.
func TestTheLeaderCardsColumnsAlign(t *testing.T) {
	row := regexp.MustCompile(`^(\S+)  (.+?) {2,}(\S+)  `)
	col := -1
	for i, line := range strings.Split(leaderText, "\n") {
		m := row.FindStringSubmatchIndex(line)
		if m == nil {
			t.Fatalf("row %d is not two commands: %q", i, line)
		}
		at := utf8.RuneCountInString(line[:m[6]])
		if col < 0 {
			col = at
		}
		if at != col {
			t.Errorf("row %d's second command starts at column %d, the first row's at %d: %q", i, at, col, line)
		}
	}
}
