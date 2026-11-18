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

// TestThePanesOverTheWire drives the example as AutoDoc does: the library's list has the keys
// first; F7 gives them to the editor, where Tab moves between the field and the button and never
// back to the list; F8 maximizes the editor and restores it; Shift+F7 returns to the library; and a
// double press on the library's title maximizes it.
func TestThePanesOverTheWire(t *testing.T) {
	h := hosttest.Start(t, newLibrary(), 60, 14, plugin.Theme{Name: "plain"})
	h.Screen("Library")
	h.Key(plugin.Key{Key: "Down"}) // the library's list has the keys: Emma
	h.Screen("editing: Emma")
	h.Key(plugin.Key{Key: "F7"}) // the editor: its field first
	h.Type("Persuasion")
	h.Screen("Persuasion█")
	h.Key(plugin.Key{Key: "Tab"}) // the button
	h.Key(plugin.Key{Key: "Tab"}) // the field again, not the library's list
	h.Type("!")
	h.Screen("Persuasion!█")
	h.Key(plugin.Key{Key: "Tab"})
	h.Key(plugin.Key{Key: "Enter"}) // Rename
	h.Screen("renamed Emma to Persuasion!")
	h.Key(plugin.Key{Key: "Tab"}) // back to the field

	h.Key(plugin.Key{Key: "F8"}) // the editor maximized
	h.Type("z")                  // a frame after F8: the field shows it
	if s := h.Screen("z█"); strings.Contains(s, "Library") {
		t.Fatalf("F8 left the library shown:\n%s", s)
	}
	h.Key(plugin.Key{Key: "F8"}) // restored
	h.Type("y")
	if s := h.Screen("zy█"); !strings.Contains(s, "Library") {
		t.Fatalf("F8 again didn't restore the library:\n%s", s)
	}
	h.Key(plugin.Key{Key: "u", Ctrl: true}) // clear the field

	h.Key(plugin.Key{Key: "F7", Shift: true}) // back to the library: Up moves its list
	h.Key(plugin.Key{Key: "Up"})
	s := h.Screen("editing: Dune")
	x, y := titleAt(t, s, "Library")
	h.PressCount(x, y, 2)          // the second press of a double click, and its release
	h.Key(plugin.Key{Key: "Down"}) // a frame after the press: the list's selection moved
	if s = h.Screen("> Persuasion!"); strings.Contains(s, "Editor") {
		t.Fatalf("a double press on the library's title didn't maximize it:\n%s", s)
	}
}

// dividerAt is the column of the divider, the one `|` on the screen's row y; -1 when the row has
// none or several.
func dividerAt(screen string, y int) int {
	rows := strings.Split(screen, "\n")
	if y >= len(rows) || strings.Count(rows[y], "|") != 1 {
		return -1
	}
	return len([]rune(rows[y][:strings.Index(rows[y], "|")]))
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
// it, one drag that jumps ten cells, and the release. It stays where it was let go, and the
// library's list still has the keys. Dragged a cell at a time past the card's right edge, it stops
// at the editor's least width.
func TestTheDividerFollowsADrag(t *testing.T) {
	h := hosttest.Start(t, newLibrary(), 60, 14, plugin.Theme{Name: "plain"})
	const y = 3
	x := dividerAt(h.Screen("Library"), y)
	if x < 0 {
		t.Fatalf("no divider on row %d:\n%s", y, h.Screen("Library"))
	}
	// one jump of ten cells, as a fast pointer moves: AutoDoc sends one drag for it
	h.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 1})
	h.Pointer(plugin.Pointer{Kind: "drag", Button: "left", X: x + 10, Y: y, Count: 1})
	h.Pointer(plugin.Pointer{Kind: "release", Button: "left", X: x + 10, Y: y, Count: 1})
	h.WaitFor("the divider ten cells right", func(s string) bool { return dividerAt(s, y) == x+10 })
	h.Key(plugin.Key{Key: "Down"}) // the list still has the keys: Emma
	h.Screen("editing: Emma")
	h.Drag(x+10, y, 70, y) // let go off the card
	s := h.WaitFor("the divider stopped short of the right edge", func(s string) bool {
		d := dividerAt(s, y)
		return d > x+10 && d < 59
	})
	if !strings.Contains(s, "Editor") {
		t.Fatalf("the editor's pane is gone after the drag:\n%s", s)
	}
}

// TestACancelEndsTheDividersDrag: AutoDoc cancels a gesture when the panel closes, a menu opens, F6
// moves the keys, or the pointer is taken back. The divider then ends its drag where it had
// reached (the native kit keeps it there), takes a later drag with no press as nothing, and a
// press elsewhere, on the library's list, selects a book and moves no divider.
func TestACancelEndsTheDividersDrag(t *testing.T) {
	h := hosttest.Start(t, newLibrary(), 60, 14, plugin.Theme{Name: "plain"})
	const y = 3
	s := h.Screen("Library")
	x := dividerAt(s, y)
	if x < 0 {
		t.Fatalf("no divider on row %d:\n%s", y, s)
	}
	h.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 1})
	h.Pointer(plugin.Pointer{Kind: "drag", Button: "left", X: x + 5, Y: y, Count: 1})
	s = h.WaitFor("the divider five cells right", func(s string) bool { return dividerAt(s, y) == x+5 })
	h.Pointer(plugin.Pointer{Kind: "cancel", Button: "left", X: x + 5, Y: y, Count: 1})
	h.Pointer(plugin.Pointer{Kind: "drag", Button: "left", X: x + 15, Y: y, Count: 1}) // no press: no drag
	bx, by := titleAt(t, s, "Ulysses")
	h.Press(bx, by)
	s = h.Screen("editing: Ulysses") // a frame after the cancel and the stray drag
	if d := dividerAt(s, y); d != x+5 {
		t.Fatalf("after the cancel, a stray drag and a press on the list, the divider is at %d; want %d, where the drag had reached:\n%s", d, x+5, s)
	}
}
