package card

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// openPanes attaches a card of root's panes to a fake host w×h, focused.
func openPanes(t *testing.T, root *Split, w, h int) (*Card, *fakeHost) {
	t.Helper()
	c := NewPanes(root, Options{Modal: true})
	host := &fakeHost{}
	c.Attach(host, w, h, themed)
	return c, host
}

func listOf(n int, name string) *List {
	return &List{Len: n, Item: func(i int) string { return fmt.Sprintf("%s %d", name, i) }}
}

// leafPane is a titled pane over the page of blocks given.
func leafPane(title string, body ...Block) Pane {
	return Pane{Title: title, Page: func() Page { return Page{Body: body} }}
}

// shows reports whether the frame's text holds s anywhere.
func shows(f *plugin.Frame, s string) bool {
	return strings.Contains(strings.Join(screen(f), "\n"), s)
}

// paneOf is the laid-out pane titled title.
func paneOf(t *testing.T, c *Card, title string) paneAt {
	t.Helper()
	for _, a := range c.panes {
		if a.p.Title == title {
			return a
		}
	}
	t.Fatalf("no pane titled %q laid out", title)
	return paneAt{}
}

func press(x, y, count int) plugin.Pointer {
	return plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: count}
}

func shiftF7() plugin.Key { return plugin.Key{Key: "F7", Shift: true} }

// F7 and Shift+F7 give the keys to the next and the previous pane in reading order, wrapping, and
// the focused pane's title is host.title. The mutant, named here: the step without wrapping.
func TestF7AndShiftF7MoveThePanesFocusAndWrap(t *testing.T) {
	left, right := listOf(3, "left"), listOf(3, "right")
	root := &Split{A: leafPane("Left", left), B: leafPane("Right", right)}
	c, host := openPanes(t, root, 40, 8)
	if c.pane != &root.A || c.Focused() != left {
		t.Fatalf("at first the keys are %v's, focus %v; want the first pane's list", c.pane, c.Focused())
	}
	c.Key(key("F7"))
	if c.pane != &root.B || c.Focused() != right || host.titles[len(host.titles)-1] != "Right" {
		t.Fatalf("after F7: pane %v, focus %v, title %v; want the right pane's", c.pane, c.Focused(), host.titles)
	}
	c.Key(key("F7"))
	if c.pane != &root.A {
		t.Fatal("F7 on the last pane didn't wrap to the first")
	}
	c.Key(shiftF7())
	if c.pane != &root.B {
		t.Fatal("Shift+F7 on the first pane didn't wrap to the last")
	}
	c.Key(shiftF7())
	if c.pane != &root.A || host.titles[len(host.titles)-1] != "Left" {
		t.Fatal("Shift+F7 didn't go back to the first pane")
	}
}

// F8 maximizes the focused pane in its Split and restores it; a left double press on a pane's
// title line does the same for that pane, and gives it the keys. The mutant, named here: the title
// double press not zooming.
func TestF8AndATitleDoublePressMaximizeAndRestore(t *testing.T) {
	root := &Split{A: leafPane("Left", listOf(2, "left")), B: leafPane("Right", listOf(2, "right"))}
	c, host := openPanes(t, root, 40, 8)
	c.Key(key("F8"))
	if f := host.last(); !shows(f, "Left") || shows(f, "Right") || shows(f, "right 0") {
		t.Fatalf("after F8 on the left pane:\n%s", strings.Join(screen(f), "\n"))
	}
	c.Key(key("F8"))
	if f := host.last(); !shows(f, "Left") || !shows(f, "Right") {
		t.Fatalf("after F8 again, not restored:\n%s", strings.Join(screen(f), "\n"))
	}
	r := paneOf(t, c, "Right")
	if !c.Pointer(press(r.x+1, r.title, 2)) {
		t.Fatal("a double press on the right pane's title wasn't used")
	}
	if f := host.last(); c.pane != &root.B || shows(f, "Left") || !shows(f, "Right") {
		t.Fatalf("a title double press: pane %v\n%s", c.pane, strings.Join(screen(f), "\n"))
	}
	r = paneOf(t, c, "Right")
	c.Pointer(press(r.x+1, r.title, 2))
	if f := host.last(); !shows(f, "Left") || !shows(f, "Right") {
		t.Fatalf("a second title double press didn't restore:\n%s", strings.Join(screen(f), "\n"))
	}
	// a single press on a title gives that pane the keys and zooms nothing
	l := paneOf(t, c, "Left")
	c.Pointer(press(l.x+1, l.title, 1))
	if f := host.last(); c.pane != &root.A || !shows(f, "Right") {
		t.Fatalf("a single title press: pane %v\n%s", c.pane, strings.Join(screen(f), "\n"))
	}
}

// Tab and Shift+Tab cycle the focused pane's controls only, wrapping within it: the keys never
// leave the pane. The mutant, named here: Tab cycling every pane's controls.
func TestTabNeverLeavesItsPane(t *testing.T) {
	list, in := listOf(3, "left"), &Input{Label: "Name"}
	right := listOf(3, "right")
	root := &Split{A: leafPane("Left", list, in), B: leafPane("Right", right)}
	c, _ := openPanes(t, root, 40, 10)
	for i, want := range []Control{in, list, in, list} {
		c.Key(key("Tab"))
		if c.pane != &root.A || c.Focused() != want {
			t.Fatalf("Tab %d: pane %v, focus %v; want the left pane's %v", i+1, c.pane, c.Focused(), want)
		}
	}
	c.Key(plugin.Key{Key: "Tab", Shift: true})
	if c.pane != &root.A || c.Focused() != in {
		t.Fatal("Shift+Tab left the pane or didn't wrap within it")
	}
	if !c.Typing() {
		t.Fatal("the focused pane's Input has the keys, but Typing says no")
	}
}

// A pane with no control, its page empty or text alone: F7 reaches it, F8 zooms it, a title double
// press focuses it, and Tab inside it does nothing (it isn't used, and the keys stay there).
func TestAPaneWithNoControlStillTakesThePanesKeys(t *testing.T) {
	for _, quiet := range []Pane{
		{Title: "Words", Page: func() Page { return Page{Body: []Block{Text(Plain, "just words")}} }},
		{Title: "Words"}, // no page at all: an empty one
	} {
		right := listOf(2, "right")
		root := &Split{A: leafPane("Right", right), B: quiet}
		c, host := openPanes(t, root, 40, 8)
		c.Key(key("F7"))
		if c.pane != &root.B || c.Focused() != nil {
			t.Fatalf("F7 didn't reach the pane with no control: pane %v focus %v", c.pane, c.Focused())
		}
		if c.Key(key("Tab")) || c.pane != &root.B {
			t.Fatal("Tab in a pane with no control was used, or moved the keys away")
		}
		c.Key(key("F8"))
		if f := host.last(); shows(f, "right 0") || !shows(f, "Words") {
			t.Fatalf("F8 didn't zoom the pane with no control:\n%s", strings.Join(screen(f), "\n"))
		}
		c.Key(key("F8"))
		c.Key(key("F7")) // to the list
		w := paneOf(t, c, "Words")
		c.Pointer(press(w.x+1, w.title, 2))
		if c.pane != &root.B {
			t.Fatal("a title double press didn't focus the pane with no control")
		}
		c.Pointer(press(w.x+1, w.title, 2))
	}
}

// The divider follows a left press on it, its drags and its release, Ratio set as it moves. A
// release off the view still ends the drag, the divider at the nearest place its sides' least sizes
// allow; cancel ends it where it is; a right release during a left drag ends nothing. The mutants,
// named here: the bounds check gating a release (a release off the view ignored, the drag going
// on); cancel moving the divider to its coordinates; any button's release ending the drag.
func TestTheDividerFollowsItsDragAndEnds(t *testing.T) {
	root := &Split{A: leafPane("Left", listOf(2, "left")), B: leafPane("Right", listOf(2, "right"))}
	c, host := openPanes(t, root, 41, 8) // 40 cells to share: the divider at 20
	ev := func(kind, button string, x int) plugin.Pointer {
		return plugin.Pointer{Kind: kind, Button: button, X: x, Y: 3, Count: 1}
	}
	at := func() int { return c.dividers[0].x }
	if at() != 20 {
		t.Fatalf("the divider at %d; want 20, a half", at())
	}
	if !c.Pointer(press(at(), 3, 1)) || c.drag == nil {
		t.Fatal("a press on the divider didn't start a drag")
	}
	c.Pointer(ev("drag", "left", 10))
	if root.Ratio != 10.0/40 || at() != 10 || screen(host.last())[3][10] != '|' {
		t.Fatalf("after a drag to 10: ratio %v, divider at %d", root.Ratio, at())
	}
	c.Pointer(ev("release", "left", -7)) // off the view: ends the drag, at the left pane's least size
	if c.drag != nil || at() != 3 {
		t.Fatalf("a release off the view: drag %v, divider at %d; want ended at 3", c.drag, at())
	}
	c.Pointer(ev("drag", "left", 15))
	if at() != 3 {
		t.Fatal("a drag after the release moved the divider")
	}

	c.Pointer(press(at(), 3, 1))
	c.Pointer(ev("drag", "left", 25))
	c.Pointer(ev("cancel", "left", 35))
	if c.drag != nil || at() != 25 {
		t.Fatalf("cancel: drag %v, divider at %d; want ended where it was, 25", c.drag, at())
	}
	c.Pointer(ev("drag", "left", 30))
	if at() != 25 {
		t.Fatal("a drag after the cancel moved the divider")
	}

	c.Pointer(press(at(), 3, 1))
	c.Pointer(ev("drag", "left", 12))
	c.Pointer(ev("release", "right", 30)) // another button's release: the left drag goes on
	c.Pointer(ev("drag", "left", 18))
	if c.drag == nil || at() != 18 {
		t.Fatalf("after a right release mid-drag: drag %v, divider at %d; want still dragging, at 18", c.drag, at())
	}
	c.Pointer(ev("release", "left", 100)) // off the right edge: the right pane's least size
	if c.drag != nil || at() != 37 {
		t.Fatalf("a release past the right edge: drag %v, divider at %d; want ended at 37", c.drag, at())
	}
	// with no drag in hand, a drag, release or cancel is no one's
	if c.Pointer(ev("release", "left", 5)) || c.Pointer(ev("cancel", "left", 5)) {
		t.Fatal("a release or cancel with no drag was used")
	}
}

// Splits nest for rows and columns: each pane is laid out at its own size, a column divider `|`
// and a row divider `-` between them; F7 reaches every leaf in reading order; F8 maximizes a pane
// in its own Split only. The mutant, named here: F8 zooming the outermost Split.
func TestNestedSplitsLayOutAndF7ReachesEveryLeaf(t *testing.T) {
	inner := &Split{Vertical: true, A: leafPane("Two", listOf(2, "two")), B: leafPane("Three", listOf(2, "three"))}
	root := &Split{A: leafPane("One", listOf(2, "one")), B: Pane{Split: inner}}
	c, host := openPanes(t, root, 40, 12)
	f := host.last()
	one, two, three := paneOf(t, c, "One"), paneOf(t, c, "Two"), paneOf(t, c, "Three")
	if one.x != 0 || two.x <= one.x || three.x != two.x || three.title <= two.y || two.w != three.w {
		t.Fatalf("the panes laid out at %+v %+v %+v", one, two, three)
	}
	rows := screen(f)
	if !strings.Contains(rows[0], "One") || !strings.Contains(rows[0], "Two") || !strings.Contains(rows[three.title], "Three") {
		t.Fatalf("the titles aren't on their rows:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[three.title-1], "---") {
		t.Fatalf("no row divider above the third pane:\n%s", strings.Join(rows, "\n"))
	}
	for _, want := range []*Pane{&inner.A, &inner.B, &root.A} {
		c.Key(key("F7"))
		if c.pane != want {
			t.Fatalf("F7 reached %v; want %v (reading order: One, Two, Three)", c.pane, want)
		}
	}
	c.Key(shiftF7()) // back to Three
	c.Key(key("F8"))
	if f := host.last(); !shows(f, "Three") || shows(f, "Two") || !shows(f, "One") {
		t.Fatalf("F8 on the third pane maximized more than it in its own Split:\n%s", strings.Join(screen(f), "\n"))
	}
}
