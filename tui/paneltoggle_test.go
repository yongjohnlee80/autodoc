//go:build linux || darwin

package tui

import (
	"testing"
)

// paneltoggle_test.go: a panel's toggle decides by whether the panel is seen whole, never by where
// the keyboard is (docs/decisions/editor.md#a-toggle-raises-a-covered-panel).

// covered is whether panel id is open but covered by a window or panel over it; read on the loop.
func (r *running) covered(id string) bool {
	return onLoop(r, func() bool { return r.h.panelCovered(id) })
}

// The todos open, the keyboard goes to the page, and the page, raised as the keyboard enters it,
// lies over them. SPC x once brings the todos to the front with the keyboard, open; once more
// closes them.
func TestATogglePressedOnACoveredPanelRaisesItInsteadOfClosingIt(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.togglePanel("todos"); return true })
	r.s.WaitFor(t, "the todos open with the keyboard", func(string) bool { return r.focusedOn("todoOpenTable") })
	if r.covered("todos") {
		t.Fatal("the todos, just opened in front, are covered")
	}

	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the page over the todos, with the keyboard", func(string) bool {
		return r.focused("editor") && r.covered("todos")
	})

	onLoop(r, func() bool { r.h.togglePanel("todos"); return true })
	r.s.WaitFor(t, "the todos in front with the keyboard", func(string) bool {
		return r.focusedOn("todoOpenTable") && !r.covered("todos")
	})
	if !r.panelIsOpen("todos") || r.hidden("todos") {
		t.Fatalf("raised: open %v, hidden %v; want open and shown", r.panelIsOpen("todos"), r.hidden("todos"))
	}

	onLoop(r, func() bool { r.h.togglePanel("todos"); return true })
	r.s.WaitFor(t, "the todos closed", func(string) bool { return !r.panelIsOpen("todos") })
}

// On whichever tab the todos show, the toggle brings a covered panel forward with the keyboard in
// that tab: the Open tab's table, off screen on another tab, takes no focus.
func TestATogglePressedOnCoveredTodosRaisesThemOnEveryTab(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.togglePanel("todos"); return true })
	r.s.WaitFor(t, "the todos open with the keyboard", func(string) bool { return r.focusedOn("todoOpenTable") })
	views := []string{"todoOpenTable", "todoInProgressTable", "todoDeferredTable", "todoCompletedTable", "todoMonths", "todoVarsTable"}
	for tab, view := range views {
		onLoop(r, func() bool { todosOf(r).showTab(tab); return true })
		r.s.WaitFor(t, view+" with the keyboard", func(string) bool { return r.focusedOn(view) })
		onLoop(r, func() bool { r.h.focusEditor(); return true })
		r.s.WaitFor(t, "the page over the todos, on "+view, func(string) bool {
			return r.focused("editor") && r.covered("todos")
		})
		onLoop(r, func() bool { r.h.togglePanel("todos"); return true })
		r.s.WaitFor(t, "the todos in front, on "+view, func(string) bool {
			return r.focusedOn(view) && !r.covered("todos")
		})
	}
}

// The explorer tiled beside the page, the keyboard in the page: nothing covers the explorer, so
// its toggle closes it at the first press, though it hasn't the keyboard.
func TestATogglePressedOnAnUncoveredPanelWithoutTheKeyboardClosesIt(t *testing.T) {
	r := tiledExplorer(t)
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the keyboard in the page", func(string) bool { return r.focused("editor") })
	if r.covered("explorer") {
		t.Fatalf("the explorer tiled beside the page is covered (explorer %+v, page %+v)", boundsOf(r, "explorer"), boundsOf(r, "editorWindow"))
	}

	onLoop(r, func() bool { r.h.togglePanel("explorer"); return true })
	r.s.WaitFor(t, "the explorer closed at the first press", func(string) bool { return !r.panelIsOpen("explorer") })
}

// The dock icon is a toggle too: on a covered panel it brings it to the front, not hides it.
func TestADockIconOnACoveredPanelRaisesIt(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.togglePanel("todos"); return true })
	r.s.WaitFor(t, "the todos open with the keyboard", func(string) bool { return r.focusedOn("todoOpenTable") })
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the page over the todos", func(string) bool { return r.focused("editor") && r.covered("todos") })

	onLoop(r, func() bool { r.h.goDock("todos"); return true })
	r.s.WaitFor(t, "the todos in front with the keyboard", func(string) bool {
		return r.focusedOn("todoOpenTable") && !r.covered("todos")
	})
	if r.hidden("todos") {
		t.Fatal("the dock icon hid the covered todos")
	}
}
