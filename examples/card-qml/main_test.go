package main

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/examples/internal/hosttest"
	"github.com/yongjohnlee80/autodoc/plugin"
)

// TestTheQMLCardOverTheWire drives the example as AutoDoc does: open, Tab into the field, type a
// name, Enter, and a theme change that keeps what the card shows.
func TestTheQMLCardOverTheWire(t *testing.T) {
	h := hosttest.Start(t, newCard(), 44, 8, plugin.Theme{Name: "a", Colors: map[string]string{"app.window": "#112233"}})
	h.Screen("Ann, Bob")
	h.Key(plugin.Key{Key: "Tab"})
	h.Type("Dan")
	h.Screen("Dan")
	h.Key(plugin.Key{Key: "Enter"})
	h.Screen("added Dan")
	h.Theme(plugin.Theme{Name: "b", Colors: map[string]string{"app.window": "#445566"}})
	h.Screen("Ann, Bob, Dan")
}
