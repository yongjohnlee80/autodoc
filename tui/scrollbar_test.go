package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestThePagesScrollbarReachesTheEnd: a note read to its end has the page's scrollbar thumb on the
// page's last row, not halfway (Johno, 2026-10-02: "The scrollbar on the right goes down only to
// the halfway, and by the time it's there we are already at the end"; golib v0.6.20).
func TestThePagesScrollbarReachesTheEnd(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	d := startDaemon(t, map[string][]string{"kb": {"long.md", b.String()}})
	r := attached(t, d)
	r.openByPicker(t, "long.md")
	r.waitNote(t, "long.md")
	r.s.WaitForText(t, "line 0")
	r.keys(t, key('G')) // Normal mode: the last line
	r.s.WaitForText(t, "line 199")
	r.s.WaitFor(t, "the thumb on the page's last row", func(sc string) bool {
		// the page's last row is the one above its frame's bottom border (the note's last line is the
		// empty one after "line 199")
		rows := strings.Split(sc, "\n")
		thumb, last := -1, -1
		for y, row := range rows {
			if strings.HasPrefix(row, "└") {
				last = y - 1
				break
			}
			if strings.Contains(row, "█") {
				thumb = y
			}
		}
		return last > 0 && thumb == last
	})
}
