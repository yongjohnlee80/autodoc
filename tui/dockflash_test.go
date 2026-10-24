package tui

import (
	"slices"
	"strings"
	"testing"
)

// flashes records each panel's flash steps (flashed), as the loop takes them, for the test's life.
func flashes(t *testing.T, r *running) func(id string) []bool {
	t.Helper()
	got := map[string][]bool{}
	onLoop(r, func() bool {
		flashed = func(id string, lit bool) { got[id] = append(got[id], lit) }
		return true
	})
	t.Cleanup(func() { onLoop(r, func() bool { flashed = nil; return true }) })
	return func(id string) []bool { return onLoop(r, func() []bool { return slices.Clone(got[id]) }) }
}

// twice is a whole flash: lit, unlit, lit, then the hidden look.
var twice = []bool{true, false, true, false}

// A panel hidden into the dock lights its icon twice, and no other; shown again mid-flash, its icon is
// its own at once and the flash ends; closed out of the dock (x), it flashes nothing.
func TestAHiddenPanelsDockIconFlashesTwice(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
	r := attached(t, d)
	steps := flashes(t, r)
	onLoop(r, func() bool { r.h.togglePanel("explorer"); return true })
	r.s.WaitFor(t, "the explorer open", func(string) bool { return r.panelIsOpen("explorer") && !r.hidden("explorer") })

	windowControl(t, r, "_")
	r.s.WaitFor(t, "_: its icon flashed twice", func(string) bool { return slices.Equal(steps("explorer"), twice) })
	if lit := steps("editor"); len(lit) != 0 {
		t.Errorf("the editor's icon flashed %v", lit)
	}
	if onLoop(r, func() bool { return r.dockFlashRow("explorer") }) {
		t.Error("the flash over, the icon is still lit")
	}

	// the dock's icon shows it, then hides it, then shows it again at once: the flash ends unlit
	onLoop(r, func() bool { r.h.goDock("explorer"); return true })
	r.s.WaitFor(t, "shown", func(string) bool { return !r.hidden("explorer") })
	lit := onLoop(r, func() [2]bool {
		r.h.goDock("explorer") // hidden: lit
		hidden := r.dockFlashRow("explorer")
		r.h.goDock("explorer") // shown again mid-flash: its own look at once
		return [2]bool{hidden, r.dockFlashRow("explorer")}
	})
	if lit != [2]bool{true, false} {
		t.Fatalf("hidden then shown mid-flash: lit %v, want lit then not", lit)
	}
	r.s.WaitFor(t, "the flash ended at its next step", func(string) bool {
		return slices.Equal(steps("explorer"), append(slices.Clone(twice), true, false)) &&
			onLoop(r, func() bool { return r.h.dockFlash["explorer"] == nil })
	})

	// a hide that fails flashes nothing, and an icon of no panel is never lit
	if onLoop(r, func() bool { r.h.hidePanel("nope"); return r.h.dockFlash["nope"] != nil || r.h.dockFlashLit("nope") }) {
		t.Error("a failed hide of no panel flashed")
	}

	windowControl(t, r, "x")
	r.s.WaitFor(t, "x closed it", func(string) bool { return !r.panelIsOpen("explorer") })
	if n := len(steps("explorer")); n != len(twice)+2 {
		t.Errorf("x flashed the closed explorer: %v", steps("explorer"))
	}
}

// The terminal's _ closes its drawer with the shell running on, into the dock: its icon flashes
// twice. Its x hangs the shell up and leaves the dock: none.
func TestTheTerminalsHideFlashesAndItsXDoesNot(t *testing.T) {
	_, r := runShellTUI(t, nil)
	steps := flashes(t, r)
	catInTheTerminal(t, r)

	windowControl(t, r, "_")
	r.s.WaitFor(t, "_: the terminal's icon flashed twice", func(string) bool { return slices.Equal(steps("terminal"), twice) })
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "shown again", func(string) bool { return r.termShown() })

	windowControl(t, r, "x")
	r.s.WaitForText(t, "stop the shells and close the terminal?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "the terminal closed and out of the dock", func(string) bool {
		return !r.panelIsOpen("terminal") && !slices.Contains(r.dockKeys(), "terminal")
	})
	if got := steps("terminal"); !slices.Equal(got, twice) {
		t.Errorf("after x the terminal's flashes are %v, want the _'s alone", got)
	}
}

// dockFlashRow reports whether the dock's row for key is lit now, as the document reads it.
func (r *running) dockFlashRow(key string) bool {
	for _, t := range r.h.dockRows.All() {
		if t.name == key {
			return r.h.dockRowOf(0, t)["flash"] == true
		}
	}
	return false
}

// The terminal's x with no shell running closes it at once, asking nothing, every tab already closing
// left to close.
func TestTheTerminalsXWithNoShellRunningClosesAtOnce(t *testing.T) {
	_, r := runShellTUI(t, nil)
	catInTheTerminal(t, r)
	onLoop(r, func() bool {
		panel, err := r.h.terminals.panel()
		if err != nil {
			t.Error(err)
			return false
		}
		for _, tab := range panel.Tabs() {
			if _, err := panel.Remove(tab.ID); err != nil {
				t.Error(err)
			}
		}
		r.h.terminals.stopAllShells() // every tab closing, none running
		return true
	})
	r.s.WaitFor(t, "closed at once, nothing asked", func(sc string) bool {
		return !r.panelIsOpen("terminal") && !strings.Contains(sc, "stop the shells")
	})
}
