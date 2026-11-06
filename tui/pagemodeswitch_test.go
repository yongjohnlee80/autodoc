package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// The page shows the window's [ Raw | Rendered ] switch on its frame's title row, at its right, while
// the note has its page, and nowhere else (not on the page's own first row); a press on Raw opens
// the text, and on Rendered the page again.
func TestThePageShowsTheRawRenderedSwitch(t *testing.T) {
	r := attached(t, startDaemon(t, map[string][]string{"kb": {"a.md", "# Larder\n\nbeans\n"}}))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the note's page", func(string) bool { return shownView(r) == widget.ViewRendered })
	var row, col int
	r.s.WaitFor(t, "the switch on the page's title row", func(string) bool {
		var ok bool
		col, row, ok = find(r.s.Backend.Snapshot(), "[ Raw | Rendered ]") // its cell column, whatever is left of it
		return ok
	})
	rows := strings.Split(r.s.String(), "\n")
	if !strings.Contains(rows[row], "┌") || strings.Count(r.s.String(), "[ Raw | Rendered ]") != 1 {
		t.Fatalf("the switch is on row %d, which isn't the page's frame's title row, or it shows twice:\n%s", row, r.s)
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: col + 2, Y: row})
	r.s.WaitFor(t, "Raw after a press on Raw", func(string) bool { return shownView(r) == widget.ViewRaw })
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: col + len("[ Raw ") + 3, Y: row})
	r.s.WaitFor(t, "the page after a press on Rendered", func(string) bool { return shownView(r) == widget.ViewRendered })
}

// A right press opens the editor's menu where the pointer is, over the page as over the text: the
// page's point is moved into the editor's frame, wherever the switch sits.
func TestThePagesMenuOpensWhereThePressIsUnderTheSwitch(t *testing.T) {
	r := attached(t, startDaemon(t, map[string][]string{"kb": {"a.md", "# Larder\n\nbeans\n\nmore\n\nand more\n"}}))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the note's page", func(string) bool { return shownView(r) == widget.ViewRendered })
	undoRow := func() int {
		y := -1
		r.s.WaitFor(t, "the menu", func(sc string) bool {
			for i, line := range strings.Split(sc, "\n") {
				if strings.Contains(line, "Undo") && y < 0 {
					y = i
				}
			}
			return y >= 0
		})
		return y
	}
	press := tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, X: 50, Y: 12}
	r.keys(t, press)
	onPage := undoRow()
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "the menu closed", func(sc string) bool { return !strings.Contains(sc, "Undo") })
	r.asText(t)
	r.keys(t, press)
	if onText := undoRow(); onText != onPage {
		t.Errorf("the menu opened at row %d over the page and %d over the text, for one press", onPage, onText)
	}
}
