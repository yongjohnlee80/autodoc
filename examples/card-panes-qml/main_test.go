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
	// a double press is two presses, as AutoDoc sends a double click: golib counts them itself
	h.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 1})
	h.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 2})
	h.Type("x") // a frame after the press: the field shows it
	if s = h.Screen("x"); strings.Contains(s, "Status") {
		t.Fatalf("a double press on the reading list's title didn't maximize it:\n%s", s)
	}
}
