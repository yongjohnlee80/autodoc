package tui

import (
	"fmt"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// pictureNote is a note with a flowchart (two boxes, a gap between them) and a pie (a round shape in
// its square of rows): pictures that leave cells unpainted.
const pictureNote = "intro text\n\n```mermaid\nflowchart LR\n  Alpha --> Beta\n```\n\n" +
	"```mermaid\npie title Pets\n  \"Dogs\" : 80\n  \"Cats\" : 20\n```\n\nafter\n"

// isPieCell reports whether s is a pie's slice cell.
func isPieCell(s string) bool { return s == "█" || s == "▓" || s == "▒" || s == "░" }

// TestANotesPicturesSitOnThePageInSepiaAndDark: in a note's Rendered view, a diagram's cells, the
// ones it paints and the ones it leaves blank (between two boxes, around a pie's round shape), wear
// the page's own background, the one the note's text sits on. None is the terminal's own colour, a
// dark block inside a sepia page.
func TestANotesPicturesSitOnThePageInSepiaAndDark(t *testing.T) {
	for _, theme := range []string{"sepia", "dark"} {
		t.Run(theme, func(t *testing.T) {
			d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", pictureNote}},
				daemonOpts{prefs: map[string]string{"tui.theme": theme}})
			r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, 40)
			r.connected(t)
			r.h.p.Post(func() { r.h.openPath("a.md") })
			var seen string
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("%s: %s\n%s", theme, seen, r.s.String())
				}
			})
			r.s.WaitFor(t, "a.md's pictures on "+theme+"'s page", func(string) bool {
				snap := r.s.Backend.Snapshot()
				// the page: under the i of "intro text", once the theme colours the note's text
				x, y, ok := find(snap, "intro text")
				if !ok || snap[y][x].Attrs.FG.Kind != tuicore.CellColorRGB {
					seen = "the note's text is not yet on the theme's colours"
					return false
				}
				page := bgIs(snap[y][x].Attrs)
				check := func(what string, x0, x1, y0, y1 int) bool {
					for yy := y0; yy <= y1; yy++ {
						for xx := x0; xx <= x1; xx++ {
							if got := bgIs(snap[yy][xx].Attrs); got != page {
								seen = fmt.Sprintf("%s's cell %q at %d,%d is on %q; the page is %q",
									what, snap[yy][xx].Content, xx, yy, got, page)
								return false
							}
						}
					}
					return true
				}
				// the flowchart: its box rows, from the first box's left edge to the last box's right
				ax, ay, ok := find(snap, "Alpha")
				if !ok || ay == 0 || !renderedByTheEditor(r) {
					seen = "the flowchart is not drawn yet"
					return false
				}
				top := snap[ay-1]
				left, right := -1, -1
				for xx, c := range top {
					if c.Content == "┌" && left < 0 && xx <= ax {
						left = xx
					}
					if c.Content == "┐" {
						right = xx
					}
				}
				if left < 0 || right <= ax || !check("the flowchart", left, right, ay-1, ay+1) {
					if left < 0 || right <= ax {
						seen = "the flowchart's boxes are not found"
					}
					return false
				}
				// the pie: every row holding a slice, across the slices' span
				minX, maxX, minY, maxY := -1, -1, -1, -1
				for yy, row := range snap {
					for xx, c := range row {
						if isPieCell(c.Content) {
							if minX < 0 || xx < minX {
								minX = xx
							}
							if xx > maxX {
								maxX = xx
							}
							if minY < 0 {
								minY = yy
							}
							maxY = yy
						}
					}
				}
				if minY < 0 || maxY-minY < 4 {
					seen = "the pie is not drawn yet"
					return false
				}
				// the legend's swatches sit in the slices' column too: stop above the legend
				if _, ly, ok := find(snap, "Dogs"); ok && ly <= maxY {
					maxY = ly - 1
				}
				return check("the pie", minX, maxX, minY, maxY)
			})
		})
	}
}
