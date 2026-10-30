package tui

import (
	"strings"
	"testing"
)

// TestADocumentsEscapeSequencesReachNoCell (AS-3): a note carrying terminal escape sequences, an
// OSC 52 clipboard write among them, opens with none of their control characters in any cell. The
// emitter writes a cell's content to the terminal as it is, so a control held by a cell would reach
// the user's terminal and act there; golib draws each one as a visible picture instead (GS-1).
func TestADocumentsEscapeSequencesReachNoCell(t *testing.T) {
	const note = "before \x1b]52;c;cHduZWQ=\x07 middle \x1b[2J\x1b]0;title\x1b\\ \u009b31m after\n"
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", note}}, daemonOpts{})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 160, 20)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitOpened(t, "after")
	for y, row := range r.s.Backend.Snapshot() {
		for x, c := range row {
			for _, ch := range c.Content {
				if ch < 0x20 || ch == 0x7f || (ch >= 0x80 && ch < 0xa0) {
					t.Fatalf("cell (%d, %d) holds control %U, which the terminal would act on:\n%s", x, y, ch, r.s.String())
				}
			}
		}
	}
	if !strings.Contains(r.s.String(), "before") {
		t.Fatalf("the note's text before the escapes is missing:\n%s", r.s.String())
	}
}
