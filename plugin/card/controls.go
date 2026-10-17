package card

import (
	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/golib/tui"
)

// keyResult is what a control did with a key.
type keyResult int

const (
	unused  keyResult = iota // not the control's: Card.Key returns false
	used                     // done; the Card draws
	prevCtl                  // focus the previous control
	nextCtl                  // focus the next control
)

// plain reports a key typed with neither Ctrl nor Alt.
func plain(k plugin.Key) bool { return !k.Ctrl && !k.Alt }

// ---------------------------------------------------------------- Input

// Input is a one-row text field, under its label. A Masked one draws each character as •, and
// its value never leaves the rune buffer to be drawn: read it with Runes and Clear it when done.
type Input struct {
	Label   string // the row above the field; "" for none
	Masked  bool   // each character drawn as •
	OnEnter func() // nil: Enter moves to the next control
	value   []rune
	at      int // the cursor, in runes, always at a character's start
}

func (*Input) block()   {}
func (*Input) control() {}

// Value is the text typed.
func (in *Input) Value() string { return string(in.value) }

// Runes is the live buffer, for a passphrase read without a string copy. It is valid until the
// next key or Clear.
func (in *Input) Runes() []rune { return in.value }

// Set replaces the text, the cursor at its end. The old runes are zeroed.
func (in *Input) Set(s string) {
	in.Clear()
	in.insert([]rune(s))
}

// Clear zeroes the runes, then empties the field.
func (in *Input) Clear() {
	clear(in.value)
	in.value, in.at = in.value[:0], 0
}

// starts are the rune offsets where the value's characters begin, and its end: grapheme
// clusters, or each rune of a Masked value (which is never made a string to be measured).
func (in *Input) starts() []int {
	out := make([]int, 0, len(in.value)+1)
	if in.Masked {
		for i := range in.value {
			out = append(out, i)
		}
		return append(out, len(in.value))
	}
	i := 0
	for g := range tui.Graphemes(string(in.value)) {
		out = append(out, i)
		i += len([]rune(g))
	}
	return append(out, i)
}

// step is the character start before (-1) or after (+1) the cursor.
func (in *Input) step(dir int) int {
	starts := in.starts()
	for i, s := range starts {
		if s == in.at {
			return starts[min(max(i+dir, 0), len(starts)-1)]
		}
	}
	return in.at
}

// insert puts rs at the cursor. A buffer that must grow is copied and the old one zeroed, so no
// stale copy of a passphrase is left behind.
func (in *Input) insert(rs []rune) {
	if len(in.value)+len(rs) > cap(in.value) {
		grown := make([]rune, len(in.value), 2*(len(in.value)+len(rs)))
		copy(grown, in.value)
		clear(in.value)
		in.value = grown
	}
	n := len(in.value)
	in.value = in.value[:n+len(rs)]
	copy(in.value[in.at+len(rs):], in.value[in.at:n])
	copy(in.value[in.at:], rs)
	in.at += len(rs)
	in.settle()
}

// settle keeps the cursor at a character's start. An edit can join the characters around it (a
// ZWJ typed between two emoji makes them one): the cursor then goes to the end of the character
// it fell inside, behind what was typed, as it is after any insertion. A Masked value is edited
// rune by rune, so every offset is a start.
func (in *Input) settle() {
	if in.Masked {
		return
	}
	for _, s := range in.starts() {
		if s >= in.at {
			in.at = s
			return
		}
	}
}

// cut removes the runes [from, to), zeroing the ones it frees.
func (in *Input) cut(from, to int) {
	if from >= to {
		return
	}
	n := len(in.value)
	copy(in.value[from:], in.value[to:])
	clear(in.value[n-(to-from) : n])
	in.value = in.value[:n-(to-from)]
	if in.at > from {
		in.at = max(from, in.at-(to-from))
	}
	in.settle() // what was left either side may now be one character
}

func (in *Input) key(k plugin.Key) keyResult {
	switch {
	case k.Key == "Up":
		return prevCtl
	case k.Key == "Down":
		return nextCtl
	case k.Key == "Enter":
		if in.OnEnter == nil {
			return nextCtl
		}
		in.OnEnter()
	case k.Key == "Backspace":
		in.cut(in.step(-1), in.at)
	case k.Key == "Delete":
		in.cut(in.at, in.step(+1))
	case k.Key == "Left":
		in.at = in.step(-1)
	case k.Key == "Right":
		in.at = in.step(+1)
	case k.Key == "Home":
		in.at = 0
	case k.Key == "End":
		in.at = len(in.value)
	case k.Ctrl && !k.Alt && k.Key == "u":
		in.Clear()
	case plain(k) && k.Text != "":
		in.insert([]rune(k.Text)) // an IME commit or a paste: inserted whole
	default:
		return unused
	}
	return used
}

// ---------------------------------------------------------------- Buttons

// Buttons are a row of buttons, "[ label ]" each, centred, wrapping as words do.
type Buttons struct {
	Labels  []string
	OnPress func(i int) // Enter, Space, or a left press
	at      int
}

func (*Buttons) block()   {}
func (*Buttons) control() {}

// Focused is the button that has the focus when the Buttons do.
func (b *Buttons) Focused() int { return b.at }

func (b *Buttons) clamp() { b.at = min(max(b.at, 0), max(len(b.Labels)-1, 0)) }

func (b *Buttons) press(i int) {
	if i >= 0 && i < len(b.Labels) && b.OnPress != nil {
		b.OnPress(i)
	}
}

func (b *Buttons) key(k plugin.Key) keyResult {
	b.clamp()
	n := len(b.Labels)
	switch {
	case k.Key == "Up":
		return prevCtl
	case k.Key == "Down":
		return nextCtl
	case k.Key == "Left" && n > 0:
		b.at = (b.at + n - 1) % n
	case k.Key == "Right" && n > 0:
		b.at = (b.at + 1) % n
	case k.Key == "Enter" || k.Key == " " && plain(k):
		b.press(b.at)
	default:
		return unused // q among them: no field has the keys, so it is the plugin's
	}
	return used
}

// ---------------------------------------------------------------- List

// List is Len rows of text, one selected. Item is asked only for the rows in view, so a list of
// thousands is never drawn whole.
type List struct {
	Len     int
	Item    func(i int) string
	Sel     int
	OnEnter func(i int)
}

func (*List) block()   {}
func (*List) control() {}

func (l *List) clamp() { l.Sel = min(max(l.Sel, 0), max(l.Len-1, 0)) }

// move is the list's and the tree's movement keys: the new index of sel among n rows, and whether
// the key was one; page is how far PageUp and PageDown go.
func move(k plugin.Key, sel, n, page int) (int, bool) {
	switch {
	case k.Key == "Up" || plain(k) && k.Key == "k":
		sel--
	case k.Key == "Down" || plain(k) && k.Key == "j":
		sel++
	case k.Key == "PageUp" || k.Ctrl && !k.Alt && k.Key == "u":
		sel -= page
	case k.Key == "PageDown" || k.Ctrl && !k.Alt && k.Key == "d":
		sel += page
	case k.Key == "Home" || plain(k) && k.Key == "g":
		sel = 0
	case k.Key == "End" || plain(k) && k.Key == "G":
		sel = n - 1
	default:
		return sel, false
	}
	return min(max(sel, 0), max(n-1, 0)), true
}

func (l *List) key(k plugin.Key, page int) keyResult {
	l.clamp()
	if sel, ok := move(k, l.Sel, l.Len, page); ok {
		l.Sel = sel
		return used
	}
	if k.Key == "Enter" {
		if l.Len > 0 && l.OnEnter != nil {
			l.OnEnter(l.Sel)
		}
		return used
	}
	return unused
}

// ---------------------------------------------------------------- Tree

// Tree is nodes under their parents, open ones showing what they hold. Its selection is a node,
// kept by identity as nodes open, close and change.
type Tree struct {
	Roots    []*Node
	OnOpen   func(n *Node) bool // a closed node opening; false keeps it closed (a load failed)
	OnEnter  func(n *Node) bool // false: Enter opens or closes the node, as on one with no handler
	sel      *Node              // the selected node; nil with selEmpty set, or before the first draw
	selEmpty *Node              // the node whose Empty row is selected
	selPath  []*Node            // the selection's ancestors, root first, when it last showed: a removed node's way back
}

// Node is one row of a Tree.
type Node struct {
	Text     string
	Leaf     bool // a row that never opens
	Open     bool
	Children []*Node
	Empty    string // shown under an open node with no children: "(no tables)"
	Value    any    // the plugin's own
}

func (*Tree) block()   {}
func (*Tree) control() {}

// trow is a row of the tree as it stands open: a node, or the Empty row of one (empty set).
type trow struct {
	n     *Node
	depth int
	empty bool
}

// rows are the tree's rows, open nodes showing what they hold.
func (t *Tree) rows() []trow {
	var out []trow
	var walk func(ns []*Node, depth int)
	walk = func(ns []*Node, depth int) {
		for _, n := range ns {
			out = append(out, trow{n: n, depth: depth})
			if !n.Open || n.Leaf {
				continue
			}
			if len(n.Children) == 0 {
				if n.Empty != "" {
					out = append(out, trow{n: n, depth: depth + 1, empty: true})
				}
				continue
			}
			walk(n.Children, depth+1)
		}
	}
	walk(t.Roots, 0)
	return out
}

// Selected is the selected node; nil when an Empty row is, or the tree is empty.
func (t *Tree) Selected() *Node {
	if t.selEmpty != nil {
		return nil
	}
	return t.sel
}

// Select makes n the selection.
func (t *Tree) Select(n *Node) { t.sel, t.selEmpty, t.selPath = n, nil, pathTo(t.Roots, n) }

// index is the selection's row among rows. A selection no longer showing (an ancestor closed, the
// node removed) moves to its nearest showing ancestor, else to the first row.
func (t *Tree) index(rows []trow) int {
	want, empty := t.sel, false
	if t.selEmpty != nil {
		want, empty = t.selEmpty, true
	}
	for i, r := range rows {
		if r.n == want && r.empty == empty {
			t.selPath = ancestors(rows, i)
			return i
		}
	}
	if empty { // the Empty row went: its node is the nearest
		for i, r := range rows {
			if r.n == want && !r.empty {
				t.sel, t.selEmpty = want, nil
				return i
			}
		}
	}
	path := pathTo(t.Roots, want) // hidden under a closed ancestor: still in the tree
	if path == nil {
		path = t.selPath // removed: the ancestors it had when it last showed
	}
	for j := len(path) - 1; j >= 0; j-- {
		for i, r := range rows {
			if r.n == path[j] && !r.empty {
				t.sel, t.selEmpty = path[j], nil
				return i
			}
		}
	}
	if len(rows) == 0 {
		t.sel, t.selEmpty = nil, nil
		return 0
	}
	t.sel, t.selEmpty = rows[0].n, nil
	return 0
}

// ancestors are the nodes holding rows[i], root first: the row above it one level up, and so on.
// An Empty row's own node is held apart (selEmpty), so its ancestors start one level up.
func ancestors(rows []trow, i int) []*Node {
	var out []*Node
	depth := rows[i].depth
	if rows[i].empty {
		depth--
	}
	for j := i - 1; j >= 0 && depth > 0; j-- {
		if rows[j].depth == depth-1 && !rows[j].empty {
			out = append([]*Node{rows[j].n}, out...)
			depth--
		}
	}
	return out
}

// pathTo is the nodes from a root down to n's parent; nil when n is not in the tree.
func pathTo(ns []*Node, n *Node) []*Node {
	for _, c := range ns {
		if c == n {
			return []*Node{}
		}
		if p := pathTo(c.Children, n); p != nil {
			return append([]*Node{c}, p...)
		}
	}
	return nil
}

func (t *Tree) selectRow(r trow) {
	if r.empty {
		t.sel, t.selEmpty = nil, r.n
	} else {
		t.sel, t.selEmpty = r.n, nil
	}
}

// open opens n, asking OnOpen first; false when OnOpen refused.
func (t *Tree) open(n *Node) bool {
	if n.Leaf || n.Open {
		return true
	}
	if t.OnOpen != nil && !t.OnOpen(n) {
		return false
	}
	n.Open = true
	return true
}

func (t *Tree) key(k plugin.Key, page int) keyResult {
	rows := t.rows()
	if len(rows) == 0 {
		if k.Key == "Enter" {
			return used
		}
		if _, ok := move(k, 0, 0, page); ok {
			return used
		}
		return unused
	}
	i := t.index(rows)
	if j, ok := move(k, i, len(rows), page); ok {
		t.selectRow(rows[j])
		return used
	}
	r := rows[i]
	switch {
	case k.Key == "Right" || plain(k) && k.Key == "l":
		switch {
		case r.empty || r.n.Leaf:
		case !r.n.Open:
			t.open(r.n)
		case i+1 < len(rows) && rows[i+1].depth > r.depth: // open: to its first row
			t.selectRow(rows[i+1])
		}
	case k.Key == "Left" || plain(k) && k.Key == "h":
		if !r.empty && !r.n.Leaf && r.n.Open {
			r.n.Open = false
			break
		}
		for j := i - 1; j >= 0; j-- { // to the row holding it
			if rows[j].depth == r.depth-1 && !rows[j].empty {
				t.selectRow(rows[j])
				break
			}
		}
	case k.Key == "Enter":
		switch {
		case r.empty:
		case t.OnEnter != nil && t.OnEnter(r.n):
		case r.n.Leaf:
		case r.n.Open:
			r.n.Open = false
		default:
			t.open(r.n)
		}
	default:
		return unused
	}
	return used
}
