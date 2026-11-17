package decl

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/decl"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/controls"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// PANES IN A DECL CARD (panes.go): F7 and Shift+F7 step the panes, Tab stays in one, F8 and a
// double press on a title maximize one, and a pane with nothing to type into is a stop of its own.

// panesCard opens a card whose program is body inside a Window, with TextFields; each field's
// Enter says its id and text (App.got), and the returned func reads what was said.
func panesCard(t *testing.T, body string, w, h int) (*Card, *fakeHost, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	c, host := start(t, Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(layoutHead+body+" }")),
		tuidecl.Singleton("hello", "1.0", "App"),
		tuidecl.Types(controls.Types()...),
		tuidecl.Handlers(map[string]decl.HandlerFunc{"App.got": func(args []qml.SpecValue) error {
			mu.Lock()
			got = append(got, args[0].Raw+"="+args[1].Raw)
			mu.Unlock()
			return nil
		}}),
	}}, w, h)
	return c, host, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

// field is a TextField whose Enter says its id and text.
func field(id string) string {
	return `TextField { id: ` + id + `; onAccepted: App.got("` + id + `", ` + id + `.text) }`
}

// typeLine types s then Enter into whatever has the card's keys.
func typeLine(c *Card, s string) {
	for _, r := range s {
		c.Key(plugin.Key{Key: string(r), Text: string(r)})
	}
	c.Key(plugin.Key{Key: "Enter"})
}

func fkey(name string, shift bool) plugin.Key { return plugin.Key{Key: name, Shift: shift} }

// saidLast waits for the last thing said to be want.
func saidLast(t *testing.T, got func() []string, want string) {
	t.Helper()
	waitFor(t, "the card said "+want, func() bool {
		g := got()
		return len(g) > 0 && g[len(g)-1] == want
	})
}

// twoPanes is pane One (fields a1, a2) beside pane Two (field b1).
var twoPanes = `Panes { Pane { title: "One"; Flex { direction: Tui.Vertical; ` + field("a1") + " " + field("a2") + ` } }
  Pane { title: "Two"; ` + field("b1") + ` } }`

// The program starts with the first pane's first control holding the keys; Tab steps that pane's
// controls and wraps inside it, never into the other pane; F7 gives the next pane the keys, and
// Shift+F7 the previous. The mutants, named here: Tab let out of the pane (it reaches b1), F7 taken
// by no Panes, and no pane given the keys at the start (the first keys reach nothing).
func TestPanesStepByF7AndTabStaysInAPane(t *testing.T) {
	c, host, got := panesCard(t, twoPanes, 40, 8)
	lastHas(t, host, "Two")
	typeLine(c, "x")
	saidLast(t, got, "a1=x")
	c.Key(plugin.Key{Key: "Tab"})
	typeLine(c, "y")
	saidLast(t, got, "a2=y")
	c.Key(plugin.Key{Key: "Tab"})
	c.Key(plugin.Key{Key: "Enter"})
	saidLast(t, got, "a1=x")
	c.Key(fkey("F7", false))
	typeLine(c, "z")
	saidLast(t, got, "b1=z")
	c.Key(fkey("F7", true))
	c.Key(plugin.Key{Key: "Enter"})
	saidLast(t, got, "a1=x")
}

// F8 maximizes the pane that has the keys, the other gone from the frame, and F8 again restores
// both. The mutant, named here: F8 zooming the wrong side.
func TestF8MaximizesThePaneWithTheKeys(t *testing.T) {
	c, host, got := panesCard(t, twoPanes, 40, 8)
	lastHas(t, host, "Two")
	c.Key(fkey("F7", false)) // pane Two
	typeLine(c, "z")
	saidLast(t, got, "b1=z")
	c.Key(fkey("F8", false))
	waitFor(t, "pane Two alone", func() bool {
		_, frames, _ := host.snapshot()
		s := text(frames[len(frames)-1])
		return strings.Contains(s, "Two") && !strings.Contains(s, "One")
	})
	c.Key(fkey("F8", false))
	waitFor(t, "both panes again", func() bool {
		_, frames, _ := host.snapshot()
		s := text(frames[len(frames)-1])
		return strings.Contains(s, "Two") && strings.Contains(s, "One")
	})
}

// A pane whose content takes no keys (a Text) is a stop of its own: the start gives it the keys,
// F8 there maximizes it, and F7 goes on to the next. The mutant, named here: a Pane never accepting focus
// (F7 passes it by, and F8 has no pane to maximize).
func TestAPaneWithNothingToTypeIntoIsAStop(t *testing.T) {
	body := `Panes { Pane { title: "Info"; Text { text: "hello" } } Pane { title: "Two"; ` + field("b1") + ` } }`
	c, host, got := panesCard(t, body, 40, 8)
	lastHas(t, host, "hello")
	c.Key(fkey("F8", false)) // Info holds the keys from the start
	waitFor(t, "pane Info alone", func() bool {
		_, frames, _ := host.snapshot()
		s := text(frames[len(frames)-1])
		return strings.Contains(s, "Info") && !strings.Contains(s, "Two")
	})
	c.Key(fkey("F8", false))
	// pane Two shown again: golib gives a pane just shown the keys after its next layout
	waitFor(t, "both panes again", func() bool {
		_, frames, _ := host.snapshot()
		s := text(frames[len(frames)-1])
		return strings.Contains(s, "Info") && strings.Contains(s, "Two")
	})
	c.Key(fkey("F7", false)) // Two
	typeLine(c, "z")
	saidLast(t, got, "b1=z")
}

// F7 steps the panes of nested Panes in reading order, round: from One, Two above Three, and back
// to One. The mutant, named here: an inner Panes taking F7 (it steps within itself alone).
func TestF7StepsNestedPanesInReadingOrder(t *testing.T) {
	body := `Panes { Pane { title: "One"; ` + field("a") + ` }
  Pane { Panes { orientation: Tui.Vertical; Pane { title: "Two"; ` + field("b") + ` } Pane { title: "Three"; ` + field("c") + ` } } } }`
	c, host, got := panesCard(t, body, 40, 12)
	var last string
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the last frame:\n%s", last)
		}
	})
	waitFor(t, "a frame holding Three", func() bool {
		_, frames, closes := host.snapshot()
		if closes > 0 {
			t.Fatalf("the card closed: %v", c.Err())
		}
		if len(frames) > 0 {
			last = text(frames[len(frames)-1])
		}
		return strings.Contains(last, "Three")
	})
	// the start gives pane One the keys; each step types its field's letter once more, so a's
	// second visit holds "aa"
	typeLine(c, "a")
	saidLast(t, got, "a=a")
	for _, step := range []struct{ typed, said string }{{"b", "b=b"}, {"c", "c=c"}, {"a", "a=aa"}} {
		c.Key(fkey("F7", false))
		typeLine(c, step.typed)
		saidLast(t, got, step.said)
	}
}

// A double press on a pane's title line maximizes that pane, and gives it the keys. The mutant,
// named here: the title press not taken (nothing maximizes).
func TestADoublePressOnAPanesTitleMaximizesIt(t *testing.T) {
	c, host, got := panesCard(t, twoPanes, 40, 8)
	f := lastHas(t, host, "Two")
	x, y := -1, -1
	for row, line := range strings.Split(text(f), "\n") {
		if i := strings.Index(line, "Two"); i >= 0 {
			x, y = len([]rune(line[:i])), row
		}
	}
	press := plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 1}
	c.Pointer(press)
	c.Pointer(press)
	waitFor(t, "pane Two alone", func() bool {
		_, frames, _ := host.snapshot()
		s := text(frames[len(frames)-1])
		return strings.Contains(s, "Two") && !strings.Contains(s, "One")
	})
	typeLine(c, "z")
	saidLast(t, got, "b1=z")
}

// A pane whose every control is disabled takes the keys itself, as one with nothing to type into
// does: the start reaches it, and F8 there maximizes it. The mutant, named here: focus() leaving
// the keys where they were when FocusInto is refused (F8 finds no pane holding them).
func TestAPaneWhoseControlsAreAllDisabledIsAStop(t *testing.T) {
	body := `Panes { Pane { title: "Off"; Button { text: "Go"; enabled: false } } Pane { title: "Two"; ` + field("b1") + ` } }`
	c, host, got := panesCard(t, body, 40, 8)
	lastHas(t, host, "Go")
	c.Key(fkey("F8", false))
	waitFor(t, "pane Off alone", func() bool {
		_, frames, _ := host.snapshot()
		s := text(frames[len(frames)-1])
		return strings.Contains(s, "Off") && !strings.Contains(s, "Two")
	})
	c.Key(fkey("F8", false))
	waitFor(t, "both panes again", func() bool {
		_, frames, _ := host.snapshot()
		s := text(frames[len(frames)-1])
		return strings.Contains(s, "Off") && strings.Contains(s, "Two")
	})
	c.Key(fkey("F7", false))
	typeLine(c, "z")
	saidLast(t, got, "b1=z")
}
