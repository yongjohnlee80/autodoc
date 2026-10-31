package tui

import (
	"strings"
	"testing"
)

// TestADocumentsEscapeSequencesReachNoCell (AS-3): a note carrying terminal escape sequences, an
// OSC 52 clipboard write among them, in its heading and its body, opens with none of their control
// characters in any cell. The emitter writes a cell's content to the terminal as it is, so a control
// held by a cell would reach the user's terminal and act there. The note's text reaches cells two
// ways, the editor and the frame title's breadcrumb, and golib's text drawing gives a control no
// width and draws none on either (it did before GS-1 too: this cell guards AutoDoc's paths, it does
// not tell golib versions apart). GS-1 (golib v0.8.16) also makes a cell written directly safe.
func TestADocumentsEscapeSequencesReachNoCell(t *testing.T) {
	const note = "# Head \x1b]52;c;cHduZWQ=\x07 tail\n\nbefore \x1b]52;c;cHduZWQ=\x07 middle \x1b[2J\x1b]0;title\x1b\\ \u009b31m after\n"
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", note}}, daemonOpts{})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 160, 20)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitOpened(t, "after")
	r.s.WaitFor(t, "the heading in the frame's title", func(string) bool { return strings.Contains(r.frameTitle(), "tail") })
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
