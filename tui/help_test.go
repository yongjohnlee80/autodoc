package tui

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTheLeaderCardsColumnsAlign: every row's second command starts in the same column, so its
// key letters line up. The last row may hold one command, when their number is odd.
func TestTheLeaderCardsColumnsAlign(t *testing.T) {
	row := regexp.MustCompile(`^(\S+)  (.+?) {2,}(\S+)  `)
	lone := regexp.MustCompile(`^\S+  \S.*\S$`)
	col := -1
	lines := strings.Split(leaderText, "\n")
	for i, line := range lines {
		m := row.FindStringSubmatchIndex(line)
		if m == nil && i == len(lines)-1 && lone.MatchString(line) && !strings.Contains(line, "   ") {
			continue
		}
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
