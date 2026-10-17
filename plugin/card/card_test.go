package card

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// fakeHost records what a Card sends.
type fakeHost struct {
	frames []*plugin.Frame
	titles []string
}

func (h *fakeHost) Frame(f *plugin.Frame) error { h.frames = append(h.frames, f); return nil }
func (h *fakeHost) Title(t string) error        { h.titles = append(h.titles, t); return nil }

func (h *fakeHost) last() *plugin.Frame { return h.frames[len(h.frames)-1] }

// screen is a frame's rows as text, trailing spaces dropped.
func screen(f *plugin.Frame) []string {
	var out []string
	for _, r := range f.Rows() {
		s := ""
		for _, run := range r {
			s += run.Text
		}
		out = append(out, strings.TrimRight(s, " "))
	}
	return out
}

// styleAt is the style of the run holding text in row y; false when no run does.
func styleAt(f *plugin.Frame, y int, text string) (plugin.Style, bool) {
	for _, run := range f.Rows()[y] {
		if strings.Contains(run.Text, text) {
			return run.Style, true
		}
	}
	return plugin.Style{}, false
}

func key(name string) plugin.Key { return plugin.Key{Key: name} }
func text(s string) plugin.Key   { return plugin.Key{Key: s, Text: s} }

var themed = plugin.Theme{Name: "test", Colors: map[string]string{
	"document.highlight": "#005f87", "document.highlightedText": "#ffffff",
	"app.inactive.highlight": "#444444", "app.inactive.highlightedText": "#87afd7",
}}

// open attaches a Card over page to a fake host w×h, focused.
func open(t *testing.T, page func() Page, w, h int) (*Card, *fakeHost) {
	t.Helper()
	c := New(page, Options{Modal: true})
	host := &fakeHost{}
	c.Attach(host, w, h, themed)
	return c, host
}

func TestFitGivesRowsInOrder(t *testing.T) {
	list := &List{Len: 50, Item: func(i int) string { return fmt.Sprintf("row %d", i) }, Sel: 20}
	ok := &Buttons{Labels: []string{"OK", "Cancel"}}
	page := func() Page {
		return Page{
			Header:  []Block{Line(Title, "Heading")},
			Body:    []Block{list},
			Buttons: ok,
			Keys:    "keys line",
			Status:  "status line",
			Problem: "problem line",
		}
	}
	// at every height the parts that fit win in fit's order, and nothing overlaps
	for h := 0; h <= 30; h++ {
		c, host := open(t, page, 40, h)
		if h == 0 {
			if len(host.frames) != 0 {
				t.Fatal("a frame of no height was sent")
			}
			continue
		}
		rows := screen(host.last())
		has := func(s string) bool {
			for _, r := range rows {
				if strings.Contains(r, s) {
					return true
				}
			}
			return false
		}
		want := []string{"Heading", "> row 20", "problem line", "[ OK ]", "keys line", "─", "status line"}
		for i, w := range want {
			if i < h && !has(w) {
				t.Fatalf("h=%d: %q missing though the first %d parts fit:\n%s", h, w, h, strings.Join(rows, "\n"))
			}
			if i >= h && has(w) {
				t.Fatalf("h=%d: %q shown before parts that come first:\n%s", h, w, strings.Join(rows, "\n"))
			}
		}
		_ = c
	}
}

func TestTabCyclesAndInputsKeepTheirKeys(t *testing.T) {
	name, pass := &Input{Label: "name"}, &Input{Label: "passphrase", Masked: true}
	var signed []string
	pass.OnEnter = func() { signed = append(signed, name.Value()+":"+string(pass.Runes())) }
	btns := &Buttons{Labels: []string{"Sign in", "Cancel"}}
	page := func() Page { return Page{Body: []Block{name, pass}, Buttons: btns} }
	c, host := open(t, page, 30, 12)
	if c.Focused() != name || !c.Typing() {
		t.Fatal("the first control does not have the focus")
	}
	for _, k := range []plugin.Key{text("q"), text("ü"), {Key: "x", Ctrl: true}, {Key: "y", Alt: true}} {
		used := c.Key(k)
		if k.Ctrl || k.Alt {
			if used {
				t.Errorf("%+v was taken as text", k)
			}
		} else if !used {
			t.Errorf("%+v not used by the Input", k)
		}
	}
	if name.Value() != "qü" {
		t.Fatalf("name = %q, want qü (q is text in a field; a modified key is not)", name.Value())
	}
	c.Key(text("한글")) // an IME commit, whole
	if name.Value() != "qü한글" {
		t.Fatalf("a multi-rune Text was not inserted whole: %q", name.Value())
	}
	c.Key(key("Enter")) // name has no OnEnter: the next control
	if c.Focused() != pass {
		t.Fatal("Enter on the name did not move to the passphrase")
	}
	c.Key(text("s"))
	c.Key(text("3"))
	if rows := screen(host.last()); !strings.Contains(strings.Join(rows, "\n"), "••") || strings.Contains(strings.Join(rows, "\n"), "s3") {
		t.Fatalf("the passphrase is drawn:\n%s", strings.Join(rows, "\n"))
	}
	c.Key(key("Enter"))
	if len(signed) != 1 || signed[0] != "qü한글:s3" {
		t.Fatalf("OnEnter saw %q", signed)
	}
	buf := pass.Runes()[:cap(pass.Runes())]
	c.Key(plugin.Key{Key: "u", Ctrl: true})
	for _, r := range buf {
		if r != 0 {
			t.Fatalf("Ctrl+U left a rune behind: %q", string(buf))
		}
	}
	c.Key(key("Tab"))
	if c.Focused() != btns || c.Typing() {
		t.Fatal("Tab from the passphrase did not reach the buttons")
	}
	if c.Key(text("q")) {
		t.Fatal("q on the buttons was used: it is the plugin's, to close")
	}
	c.Key(key("Tab"))
	if c.Focused() != name {
		t.Fatal("Tab did not wrap")
	}
	c.Key(plugin.Key{Key: "Tab", Shift: true})
	if c.Focused() != btns {
		t.Fatal("Shift+Tab did not wrap back")
	}
}

func TestEditingKeysMoveByCharacters(t *testing.T) {
	in := &Input{}
	c, _ := open(t, func() Page { return Page{Body: []Block{in}} }, 30, 5)
	c.Key(text("ae\u0301b"))
	c.Key(key("Left"))
	c.Key(key("Backspace")) // the accented e goes whole
	if in.Value() != "ab" {
		t.Fatalf("Backspace removed part of a character: %q", in.Value())
	}
	c.Key(key("Home"))
	c.Key(key("Delete"))
	c.Key(key("End"))
	c.Key(text("!"))
	if in.Value() != "b!" {
		t.Fatalf("value = %q, want b!", in.Value())
	}
}

func TestAFocusedControlThatLeavesThePageGivesTheFocusOn(t *testing.T) {
	a, b := &Input{}, &Input{}
	step := 0
	page := func() Page {
		if step == 0 {
			return Page{Body: []Block{a, b}}
		}
		return Page{Body: []Block{b}}
	}
	c, _ := open(t, page, 20, 6)
	c.FocusOn(a)
	step = 1
	c.Draw()
	if c.Focused() != b {
		t.Fatal("the focus stayed on a control no longer on the page")
	}
}

func TestAListOfThousandsMakesOnlyTheRowsInView(t *testing.T) {
	asked := map[int]bool{}
	list := &List{Len: 10_000, Item: func(i int) string { asked[i] = true; return fmt.Sprint("item ", i) }}
	c, host := open(t, func() Page { return Page{Body: []Block{list}} }, 30, 10)
	for _, k := range []plugin.Key{key("PageDown"), key("End"), key("Up"), {Key: "u", Ctrl: true}} {
		c.Key(k)
		rows := screen(host.last())
		if !strings.Contains(strings.Join(rows, "\n"), fmt.Sprint("> item ", list.Sel)) {
			t.Fatalf("after %+v the selection %d is out of view:\n%s", k, list.Sel, strings.Join(rows, "\n"))
		}
	}
	if len(asked) > 200 {
		t.Fatalf("Item was asked for %d rows", len(asked))
	}
	before := list.Sel
	c.Pointer(plugin.Pointer{Kind: "wheel", Button: "up", X: 3, Y: 3, Count: 100})
	if list.Sel != before-48 {
		t.Fatalf("a wheel step of 100 moved from %d to %d, want %d (48 at most)", before, list.Sel, before-48)
	}
	empty := &List{OnEnter: func(int) { t.Fatal("Enter on an empty list ran OnEnter") }}
	c2, _ := open(t, func() Page { return Page{Body: []Block{empty}} }, 20, 5)
	if !c2.Key(key("Enter")) {
		t.Fatal("Enter on an empty list was not used")
	}
}

func TestATreeOpensClosesAndKeepsItsSelection(t *testing.T) {
	cols := &Node{Text: "orders", Value: "table"}
	public := &Node{Text: "public", Empty: "(no tables)"}
	empty := &Node{Text: "empty", Empty: "(no tables)"}
	failing := &Node{Text: "broken"}
	tree := &Tree{Roots: []*Node{public, empty, failing}}
	tree.OnOpen = func(n *Node) bool {
		switch n {
		case public:
			n.Children = []*Node{cols}
			cols.Children = []*Node{{Text: "id int", Leaf: true}}
		case failing:
			return false
		}
		return true
	}
	var entered []string
	tree.OnEnter = func(n *Node) bool {
		if n.Value == "table" {
			entered = append(entered, n.Text)
			return true
		}
		return false
	}
	c, host := open(t, func() Page { return Page{Body: []Block{tree}} }, 30, 10)
	rows := func() string { return strings.Join(screen(host.last()), "\n") }
	c.Key(key("Right")) // public opens
	if !strings.Contains(rows(), "> ▾ public") || !strings.Contains(rows(), "  ▸ orders") {
		t.Fatalf("Right did not open public:\n%s", rows())
	}
	c.Key(key("Right")) // open: to its first row
	c.Key(key("l"))     // orders opens
	c.Key(key("Down"))
	if tree.Selected().Text != "id int" || !strings.Contains(rows(), "> ") || !strings.Contains(rows(), "      id int") {
		t.Fatalf("the column row:\n%s", rows())
	}
	c.Key(key("h")) // a leaf: to the row holding it
	if tree.Selected() != cols {
		t.Fatalf("h on a leaf selected %v", tree.Selected())
	}
	c.Key(key("Enter")) // OnEnter takes the table
	if len(entered) != 1 {
		t.Fatal("Enter on a table did not reach OnEnter")
	}
	c.Key(key("Down"))  // id int
	public.Open = false // closed beneath the selection, by the plugin
	c.Draw()
	if tree.Selected() != public {
		t.Fatalf("a hidden selection went to %v, not its nearest showing ancestor", tree.Selected())
	}
	c.Key(key("Down"))
	c.Key(key("Enter")) // empty: no handler wants it, so it opens
	if !strings.Contains(rows(), "(no tables)") {
		t.Fatalf("an open node with no children shows no Empty row:\n%s", rows())
	}
	c.Key(key("Down")) // the Empty row
	if tree.Selected() != nil {
		t.Fatal("an Empty row selected a node")
	}
	c.Key(key("Down"))
	c.Key(key("Right")) // OnOpen refuses
	if failing.Open {
		t.Fatal("a node whose OnOpen failed opened")
	}
}

func TestTheSelectionWearsTheThemesHighlight(t *testing.T) {
	list := &List{Len: 3, Item: func(i int) string { return fmt.Sprint("r", i) }}
	page := func() Page { return Page{Body: []Block{list}} }
	c := New(page, Options{})
	host := &fakeHost{}
	c.Attach(host, 20, 5, themed)
	st, _ := styleAt(host.last(), 0, "> r0")
	if st.BG != "#444444" {
		t.Fatalf("an unfocused card's selection = %+v, want the inactive highlight", st)
	}
	c.Focus(true)
	st, _ = styleAt(host.last(), 0, "> r0")
	if st.BG != "#005f87" || st.FG == "" {
		t.Fatalf("a focused card's selection = %+v, want the document highlight", st)
	}
	c.Theme(plugin.Theme{Name: "bare"})
	st, _ = styleAt(host.last(), 0, "> r0")
	if st != (plugin.Style{Bold: true}) {
		t.Fatalf("with no highlight roles the selection = %+v, want Bold", st)
	}
}

func TestAPressReachesWhatWasDrawnThere(t *testing.T) {
	in := &Input{Label: "name"}
	list := &List{Len: 5, Item: func(i int) string { return fmt.Sprint("r", i) }}
	var pressed []int
	btns := &Buttons{Labels: []string{"A", "B"}, OnPress: func(i int) { pressed = append(pressed, i) }}
	c, host := open(t, func() Page { return Page{Body: []Block{in, list}, Buttons: btns} }, 30, 12)
	rows := screen(host.last())
	find := func(s string) (int, int) {
		for y, r := range rows {
			if x := strings.Index(r, s); x >= 0 {
				return width(r[:x]), y
			}
		}
		t.Fatalf("%q not drawn:\n%s", s, strings.Join(rows, "\n"))
		return 0, 0
	}
	x, y := find("r3")
	c.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 1})
	if c.Focused() != list || list.Sel != 3 {
		t.Fatalf("a press on r3: focus %T, Sel %d", c.Focused(), list.Sel)
	}
	x, y = find("[ B ]")
	c.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x + 1, Y: y, Count: 1})
	if c.Focused() != btns || len(pressed) != 1 || pressed[0] != 1 {
		t.Fatalf("a press on B: focus %T, pressed %v", c.Focused(), pressed)
	}
	if c.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: 50, Y: 0, Count: 1}) {
		t.Fatal("a press outside the frame was used")
	}
}

func TestAFieldShowsTheTextAroundItsCursor(t *testing.T) {
	in := &Input{}
	in.Set(strings.Repeat("a", 30) + "END")
	c, host := open(t, func() Page { return Page{Body: []Block{in}} }, 12, 3)
	row := screen(host.last())[0]
	if !strings.Contains(row, "END█") || !strings.Contains(row, "…") {
		t.Fatalf("the field = %q, want its end, the cursor, and the cut marked", row)
	}
	c.Key(key("Home"))
	row = screen(host.last())[0]
	if !strings.Contains(row, "█a") || !strings.HasSuffix(strings.TrimRight(row, " "), "…") {
		t.Fatalf("the field at Home = %q", row)
	}
}

func TestWrapAndElideMeasureWidths(t *testing.T) {
	if got := elide("表表表", 5); got != "表表…" {
		t.Errorf("elide = %q", got)
	}
	if got := wrap("see /usr/local/share/autodoc/plugins now", 12); strings.Join(got, "|") != "see|/usr/local/|share/|autodoc/|plugins now" {
		t.Errorf("wrap = %q", got)
	}
	if got := wrap("表表表表", 5); strings.Join(got, "|") != "表表|表表" {
		t.Errorf("wrap of wide text = %q", got)
	}
}
