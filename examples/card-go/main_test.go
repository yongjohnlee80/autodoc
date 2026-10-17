package main

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/examples/internal/hosttest"
	"github.com/yongjohnlee80/autodoc/plugin"
)

// TestTheCardOverTheWire drives the example as AutoDoc does: open, type a name, add it, pick it
// from the list, and the pointer on a button.
func TestTheCardOverTheWire(t *testing.T) {
	h := hosttest.Start(t, newPeople(), 40, 20, plugin.Theme{Name: "plain"})
	h.Screen("add someone")
	h.Type("Dan") // the first control, the field, has the focus
	h.Key(plugin.Key{Key: "Enter"})
	screen := h.Screen("added Dan")
	if !strings.Contains(screen, "Dan") {
		t.Fatalf("Dan is not on the list:\n%s", screen)
	}
	h.Type("q") // in the field, q is text
	h.Screen("q█")
	h.Key(plugin.Key{Key: "Backspace"})
	h.Key(plugin.Key{Key: "Tab"}) // the list
	h.Key(plugin.Key{Key: "End"})
	h.Key(plugin.Key{Key: "Enter"})
	h.Screen("picked Dan")
	screen = h.Screen("[ Clear ]")
	for y, row := range strings.Split(screen, "\n") {
		if x := strings.Index(row, "[ Clear ]"); x >= 0 {
			h.Press(len([]rune(row[:x]))+2, y)
		}
	}
	h.Screen("cleared")
}
