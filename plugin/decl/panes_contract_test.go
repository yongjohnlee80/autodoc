package decl

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// WHAT A CARD OF PANES KEEPS, AND WHAT IT LEAVES: a dialog open in the card keeps its keys from
// the panes; a pane with nothing to type into is reached and left by F7 and Shift+F7 from the
// other; and the pane keys are taken only by a card that has panes.

// call runs a method of the program's object id on its loop, as a handler would.
func call(t *testing.T, c *Card, id, method string) {
	t.Helper()
	done := make(chan error, 1)
	if !c.Post(func() { done <- c.Program().Call(id, method) }) {
		t.Fatalf("%s.%s: the card is not running", id, method)
	}
	if err := <-done; err != nil {
		t.Fatalf("%s.%s: %v", id, method, err)
	}
}

// set sets a source of the program's on its loop, as a handler would.
func set(t *testing.T, c *Card, name string, v any) {
	t.Helper()
	done := make(chan error, 1)
	if !c.Post(func() { done <- c.Program().Set(name, v) }) {
		t.Fatalf("set %s: the card is not running", name)
	}
	if err := <-done; err != nil {
		t.Fatalf("set %s: %v", name, err)
	}
}

// A dialog open in a card of panes holds its keys: F7, Shift+F7 and F8 move nothing out of it, and
// Tab stays in it, since none reaches the panes from inside the dialog. Closed, both panes are
// shown (F8 maximized neither), the keys are back in the pane that had them, and F7 steps again.
// The dialog's keys never pass a Panes, which sees only what bubbles from its own subtree; the cell
// holds that against a Panes that took the pane keys from the Window instead.
func TestADialogInACardOfPanesKeepsItsKeys(t *testing.T) {
	body := twoPanes + `
  Dialog { id: dlg; title: "Ask"; width: 30; ` + field("d1") + ` }`
	c, host, got := panesCard(t, body, 50, 12)
	lastHas(t, host, "Two")
	typeLine(c, "x")
	saidLast(t, got, "a1=x") // pane One holds the keys
	call(t, c, "dlg", "open")
	lastHas(t, host, "Ask")
	typeLine(c, "m")
	saidLast(t, got, "d1=m")
	for i, k := range []plugin.Key{fkey("F7", false), fkey("F7", true), fkey("F8", false), {Key: "Tab"}, {Key: "Tab", Shift: true}} {
		c.Key(k)
		typeLine(c, "n") // the dialog's field, which says its whole text on Enter
		saidLast(t, got, "d1=m"+strings.Repeat("n", i+1))
	}
	call(t, c, "dlg", "close")
	// the dialog's scrim hides the panes while it is open: once it is gone, both are shown, so F8
	// in the dialog maximized neither
	waitFor(t, "the dialog gone and both panes shown", func() bool {
		_, frames, _ := host.snapshot()
		s := text(frames[len(frames)-1])
		return !strings.Contains(s, "Ask") && strings.Contains(s, "One") && strings.Contains(s, "Two")
	})
	c.Key(plugin.Key{Key: "Enter"})
	saidLast(t, got, "a1=x") // back in the pane that had the keys
	c.Key(fkey("F7", false))
	typeLine(c, "z")
	saidLast(t, got, "b1=z")
}

// A pane with nothing to type into, reached from the other pane by F7 and by Shift+F7, takes the
// keys itself, keeps Tab (nothing to step to, and nothing let out), and is left by F7 and Shift+F7
// again. The mutant, named here: a Pane that is never a stop (F7 from Two lands back on Two).
func TestAPaneWithNothingToTypeIntoIsReachedAndLeft(t *testing.T) {
	body := `Panes { Pane { title: "Two"; ` + field("b1") + ` } Pane { title: "Info"; Text { text: "hello" } } }`
	c, host, got := panesCard(t, body, 40, 8)
	lastHas(t, host, "hello")
	typeLine(c, "a")
	saidLast(t, got, "b1=a")                  // the start: the first pane, Two
	for _, dir := range []bool{false, true} { // to Info by F7, then by Shift+F7
		c.Key(fkey("F7", dir))
		c.Key(plugin.Key{Key: "Tab"})
		c.Key(plugin.Key{Key: "Tab", Shift: true})
		typeLine(c, "q") // Info takes it, and nothing types
		c.Key(fkey("F7", !dir))
		typeLine(c, "b")
		saidLast(t, got, "b1=a"+strings.Repeat("b", map[bool]int{false: 1, true: 2}[dir]))
	}
	for _, g := range got() {
		if strings.Contains(g, "q") {
			t.Fatalf("typing while the text-only pane held the keys reached a field: %v", got())
		}
	}
}

// The pane keys are a card of panes' own: a card with no Panes is sent F7 and F8 as any other key,
// so its program's Shortcut on F7 runs; a card with Panes steps its panes on F7 instead. The mutant,
// named here: the pane keys taken by every decl card.
func TestOnlyACardOfPanesTakesThePaneKeys(t *testing.T) {
	plain := field("f") + `
  Shortcut { sequence: "F7"; onActivated: App.got("shortcut", "f7") }`
	c, host, got := panesCard(t, plain, 40, 6)
	lastHas(t, host, "")
	c.Key(fkey("F7", false))
	saidLast(t, got, "shortcut=f7")

	withPanes := twoPanes + `
  Shortcut { sequence: "F7"; onActivated: App.got("shortcut", "f7") }`
	c2, host2, got2 := panesCard(t, withPanes, 40, 8)
	lastHas(t, host2, "Two")
	c2.Key(fkey("F7", false))
	typeLine(c2, "z")
	saidLast(t, got2, "b1=z")
	for _, g := range got2() {
		if g == "shortcut=f7" {
			t.Fatal("a card of panes let F7 through to its Shortcut as well as stepping the panes")
		}
	}
}

// A dialog over a card of panes keeps Tab within itself: Tab and Shift+Tab move between its two
// fields, as its trap has them, and no pane takes Tab from it, since a key bubbles up from inside a
// trap no further than the trap (golib's routing ceiling). A Dialog opens over a Window only (golib
// refuses one declared inside a Pane), so this is every dialog a card of panes can show. Closed,
// the keys are back in the pane's field.
func TestTabInADialogOverACardOfPanesMovesBetweenItsFields(t *testing.T) {
	body := twoPanes + `
  Dialog { id: dlg; title: "Ask"; width: 30; Flex { direction: Tui.Vertical; ` + field("d1") + " " + field("d2") + ` } }`
	c, host, got := panesCard(t, body, 50, 12)
	lastHas(t, host, "Two")
	call(t, c, "dlg", "open")
	lastHas(t, host, "Ask")
	typeLine(c, "m")
	saidLast(t, got, "d1=m")
	c.Key(plugin.Key{Key: "Tab"})
	typeLine(c, "n")
	saidLast(t, got, "d2=n")
	c.Key(plugin.Key{Key: "Tab", Shift: true})
	typeLine(c, "o")
	saidLast(t, got, "d1=mo")
	c.Key(plugin.Key{Key: "Tab"})
	c.Key(plugin.Key{Key: "Tab"}) // round, within the dialog
	typeLine(c, "q")
	saidLast(t, got, "d1=moq")
	call(t, c, "dlg", "close")
	waitFor(t, "the dialog gone", func() bool {
		_, frames, _ := host.snapshot()
		return !strings.Contains(text(frames[len(frames)-1]), "Ask")
	})
	typeLine(c, "p")
	saidLast(t, got, "a1=p") // the keys are back in pane One's first field
}

// A Panes nested in the first Pane: the outermost Panes still gives the keys to the first leaf at
// the start (each Panes registers its start under the same key, which golib keeps per node), and the
// inner one leaves the start to it.
func TestTheStartReachesTheFirstLeafOfNestedPanes(t *testing.T) {
	body := `Panes { Pane { title: "Outer"; Panes { orientation: Tui.Vertical
      Pane { title: "A"; ` + field("a") + ` } Pane { title: "B"; ` + field("b") + ` } } }
  Pane { title: "C"; ` + field("c") + ` } }`
	c, host, got := panesCard(t, body, 50, 12)
	lastHas(t, host, "C")
	typeLine(c, "s")
	saidLast(t, got, "a=s")
	c.Key(fkey("F7", false))
	typeLine(c, "t")
	saidLast(t, got, "b=t") // reading order: A, B (nested), then C
	c.Key(fkey("F7", false))
	typeLine(c, "u")
	saidLast(t, got, "c=u")
}

// A pane the keys left lets go of being a stop of its own: once its control is enabled, a press on
// its frame (no control there) doesn't give the pane itself the keys, which stay where they were.
// The mutant, named here: self never cleared (the press takes the keys to the empty pane, and the
// typing after it reaches no field).
func TestAPaneTheKeysLeftIsAStopOnlyWhileItHoldsNone(t *testing.T) {
	body := `Panes { Pane { title: "Off"; Button { text: "Go"; enabled: App.on } } Pane { title: "Two"; ` + field("b1") + ` } }`
	c, host, got := panesCardWith(t, body, 40, 8, map[string]any{"App.on": false})
	lastHas(t, host, "Go")
	c.Key(fkey("F7", false)) // from Off (its own stop: its one control disabled) to Two
	typeLine(c, "a")
	saidLast(t, got, "b1=a")
	set(t, c, "App.on", true)
	// a press on pane Off's left border, inside the card: no control there
	c.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: 0, Y: 3, Count: 1})
	typeLine(c, "b")
	saidLast(t, got, "b1=ab")
}
