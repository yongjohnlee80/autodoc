package main

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/examples/internal/hosttest"
	"github.com/yongjohnlee80/autodoc/plugin"
)

// titleAt is the cell of title's first letter in screen.
func titleAt(t *testing.T, screen, title string) (x, y int) {
	t.Helper()
	for y, row := range strings.Split(screen, "\n") {
		if i := strings.Index(row, title); i >= 0 {
			return len([]rune(row[:i])), y
		}
	}
	t.Fatalf("no %q on the screen:\n%s", title, screen)
	return 0, 0
}

// TestThePanesOverTheWire drives the example as AutoDoc does:
//   - the reading list's field has the keys first, and a book typed there is added;
//   - F7 gives the keys to the status pane, which has nothing to type into, so a letter typed
//     there changes nothing;
//   - F8 maximizes the status pane and restores it;
//   - Shift+F7 returns to the reading list, whose one field Tab never leaves;
//   - a double press on the reading list's title maximizes it.
func TestThePanesOverTheWire(t *testing.T) {
	h := hosttest.Start(t, newReadingList(), 60, 12, plugin.Theme{Name: "plain"})
	h.Screen("Reading list")
	h.Screen("2 books")
	h.Type("Persuasion")
	h.Key(plugin.Key{Key: "Enter"})
	h.Screen("3 books")

	h.Key(plugin.Key{Key: "F7"}) // the status pane
	h.Type("q")
	h.Key(plugin.Key{Key: "F8"}) // the status pane maximized: a frame after the letter
	// a row starting "│3 books" is the status pane filling the card: a frame after F8 (Screen
	// returns the last frame read when it already holds what is asked for)
	if s := h.Screen("\n│3 books"); strings.Contains(s, "Reading list") || strings.Contains(s, "q") {
		t.Fatalf("F8 in the status pane left the reading list shown, or the letter typed somewhere:\n%s", s)
	}
	h.Key(plugin.Key{Key: "F8"}) // restored: the reading list at the left again
	h.Screen("\n│Dune, Emma")

	h.Key(plugin.Key{Key: "F7", Shift: true}) // back to the reading list's field
	h.Key(plugin.Key{Key: "Tab"})             // one field: Tab stays on it
	h.Key(plugin.Key{Key: "u", Ctrl: true})   // clear the field
	h.Type("Dune II")
	h.Key(plugin.Key{Key: "Enter"})
	s := h.Screen("Dune, Emma, Persuasion, Dune II")

	x, y := titleAt(t, s, "Reading list")
	// a double click is two clicks, each a press and its release at protocol 12, as AutoDoc sends
	// one: golib counts them itself
	h.Press(x, y)
	h.PressCount(x, y, 2)
	h.Type("x") // a frame after the press: the field shows it
	if s = h.Screen("x"); strings.Contains(s, "Status") {
		t.Fatalf("a double press on the reading list's title didn't maximize it:\n%s", s)
	}
}

// dividerAt is the divider's column on the screen's row y: the middle of "│││", the left pane's
// right border, the divider and the right pane's left border; -1 when the row has none.
func dividerAt(screen string, y int) int {
	rows := strings.Split(screen, "\n")
	if y >= len(rows) {
		return -1
	}
	i := strings.Index(rows[y], "│││")
	if i < 0 {
		return -1
	}
	return len([]rune(rows[y][:i])) + 1
}

// TestTheManifestShowsTheWindowIcons: AutoDoc reads the example's plugin.toml at protocol 12, with
// hide, maximize and close on its frame.
func TestTheManifestShowsTheWindowIcons(t *testing.T) {
	m := hosttest.Manifest(t)
	if m.Protocol != 12 {
		t.Fatalf("protocol %d; want 12, for the divider's drag and the icons", m.Protocol)
	}
	for _, c := range []string{"hide", "maximize", "close"} {
		if !m.Controls(c) {
			t.Fatalf("[window] controls don't list %q: %v", c, m.Window.Controls)
		}
	}
}

// TestTheDividerFollowsADrag drives the divider as AutoDoc sends a drag at protocol 12: a press on
// it, a drag a cell at a time, and the release. It stays where it was let go, and the reading
// list's field still has the keys. Dragged past the card's left edge, it stops at the reading
// list's least width.
func TestTheDividerFollowsADrag(t *testing.T) {
	h := hosttest.Start(t, newReadingList(), 60, 12, plugin.Theme{Name: "plain"})
	const y = 3
	x := dividerAt(h.Screen("2 books"), y)
	if x < 0 {
		t.Fatalf("no divider on row %d:\n%s", y, h.Screen("2 books"))
	}
	h.Drag(x, y, x-10, y)
	h.WaitFor("the divider ten cells left", func(s string) bool { return dividerAt(s, y) == x-10 })
	h.Type("Persuasion") // the field still has the keys
	h.Key(plugin.Key{Key: "Enter"})
	h.Screen("3 books")
	h.Drag(x-10, y, -9, y) // let go off the card
	h.WaitFor("the divider stopped short of the left edge", func(s string) bool {
		d := dividerAt(s, y)
		return d > 0 && d < x-10
	})
}

// TestACancelEndsTheDividersDrag: AutoDoc cancels a gesture when the panel closes, a menu opens, F6
// moves the keys, or the pointer is taken back. The divider then ends its drag where it had
// reached (golib's split keeps a revoked drag's division), takes a later drag with no press as
// nothing, and a press elsewhere, on the reading list's field, moves no divider.
func TestACancelEndsTheDividersDrag(t *testing.T) {
	h := hosttest.Start(t, newReadingList(), 60, 12, plugin.Theme{Name: "plain"})
	const y = 3
	s := h.Screen("2 books")
	x := dividerAt(s, y)
	if x < 0 {
		t.Fatalf("no divider on row %d:\n%s", y, s)
	}
	h.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 1})
	h.Pointer(plugin.Pointer{Kind: "drag", Button: "left", X: x - 5, Y: y, Count: 1})
	s = h.WaitFor("the divider five cells left", func(s string) bool { return dividerAt(s, y) == x-5 })
	h.Pointer(plugin.Pointer{Kind: "cancel", Button: "left", X: x - 5, Y: y, Count: 1})
	h.Pointer(plugin.Pointer{Kind: "drag", Button: "left", X: x - 15, Y: y, Count: 1}) // no press: no drag
	fx, fy := titleAt(t, s, "add a book")
	h.Press(fx, fy+1) // the field, below its hint
	h.Type("Persuasion")
	h.Key(plugin.Key{Key: "Enter"})
	s = h.Screen("3 books") // a frame after the cancel, the stray drag and the press
	if d := dividerAt(s, y); d != x-5 {
		t.Fatalf("after the cancel, a stray drag and a press on the field, the divider is at %d; want %d, where the drag had reached:\n%s", d, x-5, s)
	}
}
