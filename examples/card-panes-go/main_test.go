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
	h.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 2})
	h.Key(plugin.Key{Key: "Down"}) // a frame after the press: the list's selection moved
	if s = h.Screen("> Persuasion!"); strings.Contains(s, "Editor") {
		t.Fatalf("a double press on the library's title didn't maximize it:\n%s", s)
	}
}
