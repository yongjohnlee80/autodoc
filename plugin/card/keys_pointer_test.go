package card

import (
	"fmt"
	"testing"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// The buttons' keys: Left and Right move among them and wrap, Enter and Space press the one
// selected, Up and Down give the focus to the control before or after them, and a key they do not
// use (q) is the plugin's: Key answers false and nothing is pressed.
func TestTheButtonsKeys(t *testing.T) {
	in := &Input{Label: "name"}
	var pressed []int
	btns := &Buttons{Labels: []string{"A", "B", "C"}, OnPress: func(i int) { pressed = append(pressed, i) }}
	c, _ := open(t, func() Page { return Page{Body: []Block{in}, Buttons: btns} }, 30, 10)
	c.FocusOn(btns)
	for _, want := range []int{1, 2, 0} {
		if !c.Key(key("Right")) || btns.at != want {
			t.Fatalf("Right: at %d, want %d", btns.at, want)
		}
	}
	if !c.Key(key("Left")) || btns.at != 2 {
		t.Fatalf("Left from the first wraps to the last: at %d", btns.at)
	}
	c.Key(key("Enter"))
	c.Key(plugin.Key{Key: " ", Text: " "})
	if len(pressed) != 2 || pressed[0] != 2 || pressed[1] != 2 {
		t.Fatalf("Enter and Space press the selected button: %v", pressed)
	}
	if c.Key(text("q")) || len(pressed) != 2 {
		t.Fatalf("q was used, or pressed: %v", pressed)
	}
	if c.Focused() != btns {
		t.Fatalf("an unused key moved the focus to %T", c.Focused())
	}
	c.Key(key("Up"))
	if c.Focused() != in {
		t.Fatalf("Up from the buttons: focus %T, want the input before them", c.Focused())
	}
	c.FocusOn(btns)
	c.Key(key("Down"))
	if c.Focused() != in { // after the last control, the cycle wraps to the first
		t.Fatalf("Down from the buttons: focus %T, want the first control", c.Focused())
	}
}

// A wheel step moves the focused list's selection three rows a step, at most 48 however fast the
// wheel spins, and never past either end. A press or step outside the frame, or with no count, is
// not the card's.
func TestTheWheelMovesTheFocusedSelection(t *testing.T) {
	list := &List{Len: 100, Item: func(i int) string { return fmt.Sprint("r", i) }}
	c, _ := open(t, func() Page { return Page{Body: []Block{list}} }, 30, 12)
	c.FocusOn(list)
	wheel := func(button string, count int) bool {
		return c.Pointer(plugin.Pointer{Kind: "wheel", Button: button, X: 2, Y: 1, Count: count})
	}
	if !wheel("down", 1) || list.Sel != 3 {
		t.Fatalf("one step down: Sel %d, want 3", list.Sel)
	}
	if !wheel("down", 40) || list.Sel != 51 {
		t.Fatalf("a fast spin down: Sel %d, want 3+48", list.Sel)
	}
	wheel("down", 40)
	wheel("down", 40)
	if list.Sel != 99 {
		t.Fatalf("past the end: Sel %d, want the last row", list.Sel)
	}
	wheel("up", 100)
	wheel("up", 100)
	wheel("up", 100)
	if list.Sel != 0 {
		t.Fatalf("past the start: Sel %d, want 0", list.Sel)
	}
	for _, p := range []plugin.Pointer{
		{Kind: "wheel", Button: "down", X: 99, Y: 1, Count: 1},
		{Kind: "wheel", Button: "down", X: 2, Y: 99, Count: 1},
		{Kind: "wheel", Button: "down", X: -1, Y: 1, Count: 1},
		{Kind: "wheel", Button: "down", X: 2, Y: 1, Count: 0},
		{Kind: "wheel", Button: "left", X: 2, Y: 1, Count: 1},
		{Kind: "press", Button: "right", X: 2, Y: 1, Count: 1},
	} {
		if c.Pointer(p) || list.Sel != 0 {
			t.Errorf("%+v was used: Sel %d", p, list.Sel)
		}
	}
}

// A wheel step over a focused tree moves its selection by its rows, clamped, and a tree with no
// rows ignores it; with neither a list nor a tree focused, the step scrolls the body.
func TestTheWheelOnATreeAndOnTheBody(t *testing.T) {
	var kids []*Node
	for i := range 10 {
		kids = append(kids, &Node{Text: fmt.Sprint("n", i), Leaf: true})
	}
	tree := &Tree{Roots: kids}
	c, _ := open(t, func() Page { return Page{Body: []Block{tree}} }, 30, 8)
	c.FocusOn(tree)
	c.Pointer(plugin.Pointer{Kind: "wheel", Button: "down", X: 2, Y: 1, Count: 1})
	if rows := tree.rows(); tree.index(rows) != 3 {
		t.Fatalf("one step down the tree: row %d, want 3", tree.index(rows))
	}
	c.Pointer(plugin.Pointer{Kind: "wheel", Button: "down", X: 2, Y: 1, Count: 16})
	if rows := tree.rows(); tree.index(rows) != len(rows)-1 {
		t.Fatalf("past the tree's end: row %d of %d", tree.index(rows), len(rows))
	}

	empty := &Tree{}
	c2, _ := open(t, func() Page { return Page{Body: []Block{empty}} }, 30, 8)
	c2.FocusOn(empty)
	if c2.Pointer(plugin.Pointer{Kind: "wheel", Button: "down", X: 2, Y: 1, Count: 1}) {
		t.Error("a step over an empty tree was used")
	}

	in := &Input{Label: "name"}
	var body []Block
	body = append(body, in)
	for i := range 30 {
		body = append(body, Text(Plain, fmt.Sprint("line ", i)))
	}
	c3, _ := open(t, func() Page { return Page{Body: body} }, 30, 8)
	c3.FocusOn(in)
	if !c3.Pointer(plugin.Pointer{Kind: "wheel", Button: "down", X: 2, Y: 1, Count: 2}) || c3.from == 0 {
		t.Errorf("a step with the input focused: from %d, want the body scrolled", c3.from)
	}
}

// A tree with no rows uses Enter and the moving keys (there is nothing to move to, but they are
// its), and leaves any other key to the plugin.
func TestAnEmptyTreesKeys(t *testing.T) {
	tree := &Tree{}
	c, _ := open(t, func() Page { return Page{Body: []Block{tree}} }, 30, 8)
	c.FocusOn(tree)
	for _, k := range []plugin.Key{key("Enter"), key("Down"), key("Home")} {
		if !c.Key(k) {
			t.Errorf("%q on an empty tree was not used", k.Key)
		}
	}
	if c.Key(text("x")) {
		t.Error("x on an empty tree was used")
	}
}
