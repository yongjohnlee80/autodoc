package card

import (
	"math"
	"slices"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// Split is two panes, side by side or stacked; nest one in a Pane for rows and columns. The plugin
// keeps it between draws, as it keeps its controls: dragging the divider sets Ratio. The panes are
// the kit's, drawn into the one frame the plugin sends.
type Split struct {
	Vertical bool    // false: A left of B; true: A above B
	Ratio    float64 // A's share; 0 is a half; kept within the panes' least sizes
	A, B     Pane

	zoom int // the side maximized: zoomNone, zoomA or zoomB
}

// The side of a Split maximized (F8, or a title double press).
const (
	zoomNone = iota
	zoomA
	zoomB
)

// Pane is one side of a Split: a page, or a Split of its own.
type Pane struct {
	Title string      // its title line; "" for none. The focused pane's title is host.title.
	Page  func() Page // asked for afresh on every draw; nil when Split is set
	Split *Split
}

// NewPanes is a Card showing root's panes. New(page, o) stays the one-pane card.
//
// Each pane is a page of its own, laid out at its own size, with its own focus and scroll. The
// panes' keys and pointer:
//
//	F7, Shift+F7        the next or previous pane takes the keys, in reading order, wrapping
//	F8                  the focused pane maximized in its Split, or restored
//	Tab, Shift+Tab      the focused pane's controls only
//	press, left         in a pane: it takes the keys, then the press goes to it as before
//	press, left, ×2     on a pane's title line: F8 for that pane
//	press on a divider, drag, release    the divider follows, Ratio set (protocol 12's pointer)
//	cancel              the drag ends where it is
//
// A drag's or release's coordinates may lie off the view: the divider is clamped to the panes'
// least sizes, never the event.
func NewPanes(root *Split, o Options) *Card {
	return &Card{root: root, o: o, focused: o.Modal, leaves: map[*Pane]*leaf{}}
}

// leaf is a pane showing a page: a Card of its own, drawing to a frame the outer card copies in.
type leaf struct {
	card *Card
	host *capture
}

// capture is a leaf card's host: it keeps the last frame, and drops the title (the outer card
// sends the focused pane's).
type capture struct{ f *plugin.Frame }

func (h *capture) Frame(f *plugin.Frame) error { h.f = f; return nil }
func (h *capture) Title(string) error          { return nil }

// paneAt is a leaf pane as the last draw laid it out: its title row (when it has a title) and its
// content's rectangle.
type paneAt struct {
	p          *Pane
	x, y, w, h int // the content's cells, under the title row
	title      int // the title row; -1 for none
}

// divider is a Split's divider as the last draw laid it out: the cell row or column it is, and the
// Split's extent along the axis it moves on, which a drag positions it within.
type divider struct {
	s          *Split
	x, y, w, h int // the divider's cells: one column (side by side) or one row (stacked)
	from, n    int // the Split's start and size along its axis
	minA, minB int // the least sizes of its sides along that axis
}

// dragging is a divider drag in progress, from a left press on it until a left release or a cancel.
type dragging struct{ s *Split }

// leafOf is p's leaf card, made on first use.
func (c *Card) leafOf(p *Pane) *leaf {
	if lf, ok := c.leaves[p]; ok {
		return lf
	}
	page := p.Page
	if page == nil {
		page = func() Page { return Page{} }
	}
	lf := &leaf{card: New(page, Options{}), host: &capture{}}
	c.leaves[p] = lf
	return lf
}

// minAlong is the least cells pane p needs along an axis: a pane keeps a title and a row, and
// one cell of content between its margins.
func minAlong(p *Pane, vertical bool) int {
	if p.Split == nil {
		if vertical {
			if p.Title != "" {
				return 2
			}
			return 1
		}
		return 3
	}
	s := p.Split
	a, b := minAlong(&s.A, vertical), minAlong(&s.B, vertical)
	switch {
	case s.zoom == zoomA:
		return a
	case s.zoom == zoomB:
		return b
	case s.Vertical == vertical:
		return a + 1 + b
	}
	return max(a, b)
}

// share is A's cells of a Split n cells along its axis (the divider's one cell not counted):
// Ratio's share (a half for 0), kept within both sides' least sizes as far as they fit.
func (s *Split) share(n, minA, minB int) int {
	avail := n - 1
	r := s.Ratio
	if r <= 0 || r >= 1 {
		r = 0.5
	}
	a := int(math.Round(r * float64(avail)))
	a = min(a, avail-minB)
	return max(a, min(minA, avail))
}

// layPanes lays pane p into the rectangle given, appending its leaves in reading order and its
// dividers.
func (c *Card) layPanes(p *Pane, x, y, w, h int, out *[]paneAt, divs *[]divider) {
	if w <= 0 || h <= 0 {
		return
	}
	if p.Split == nil {
		at := paneAt{p: p, x: x, y: y, w: w, h: h, title: -1}
		if p.Title != "" {
			at.title, at.y, at.h = y, y+1, h-1
		}
		*out = append(*out, at)
		return
	}
	s := p.Split
	switch s.zoom {
	case zoomA:
		c.layPanes(&s.A, x, y, w, h, out, divs)
		return
	case zoomB:
		c.layPanes(&s.B, x, y, w, h, out, divs)
		return
	}
	minA, minB := minAlong(&s.A, s.Vertical), minAlong(&s.B, s.Vertical)
	if s.Vertical {
		a := s.share(h, minA, minB)
		c.layPanes(&s.A, x, y, w, a, out, divs)
		*divs = append(*divs, divider{s: s, x: x, y: y + a, w: w, h: 1, from: y, n: h, minA: minA, minB: minB})
		c.layPanes(&s.B, x, y+a+1, w, h-a-1, out, divs)
		return
	}
	a := s.share(w, minA, minB)
	c.layPanes(&s.A, x, y, a, h, out, divs)
	*divs = append(*divs, divider{s: s, x: x + a, y: y, w: 1, h: h, from: x, n: w, minA: minA, minB: minB})
	c.layPanes(&s.B, x+a+1, y, w-a-1, h, out, divs)
}

// drawPanes is Draw for a card of panes: each pane's page drawn at its own size into the one
// frame, its title above it, the dividers between; and the focused pane's title as host.title.
func (c *Card) drawPanes() {
	var at []paneAt
	var divs []divider
	c.layPanes(&Pane{Split: c.root}, 0, 0, c.w, c.h, &at, &divs)
	c.panes, c.dividers = at, divs
	c.settlePane()
	if c.pane != nil && c.pane.Title != "" && c.pane.Title != c.titled {
		if c.host.Title(c.pane.Title) == nil {
			c.titled = c.pane.Title
		}
	}
	if c.w <= 0 || c.h <= 0 {
		return
	}
	f := plugin.NewFrame(c.w, c.h)
	for _, d := range divs {
		mark := '|'
		if d.s.Vertical {
			mark = '-'
		}
		f.Fill(d.x, d.y, d.w, d.h, mark, Dim.style())
	}
	for _, a := range at {
		focused := a.p == c.pane
		if a.title >= 0 {
			st := Dim.style()
			if focused {
				st = c.selection(c.focused)
				st.Bold = true
			}
			f.Text(a.x, a.title, pad(elide(" "+a.p.Title, a.w), a.w), st)
		}
		lf := c.leafOf(a.p)
		lf.card.host, lf.card.w, lf.card.h, lf.card.theme = lf.host, a.w, a.h, c.theme
		lf.card.focused = c.focused && focused
		lf.host.f = nil
		lf.card.Draw()
		if lf.host.f == nil {
			continue
		}
		for y, row := range lf.host.f.Rows() {
			x := a.x
			for _, run := range row {
				x = f.Text(x, a.y+y, run.Text, run.Style)
			}
		}
	}
	_ = c.host.Frame(f)
}

// settlePane keeps the keys on a pane shown: the first, when the focused one isn't.
func (c *Card) settlePane() {
	if slices.ContainsFunc(c.panes, func(a paneAt) bool { return a.p == c.pane }) {
		return
	}
	c.pane = nil
	if len(c.panes) > 0 {
		c.pane = c.panes[0].p
	}
}

// stepPane gives the keys to the pane dir panes on in reading order, wrapping.
func (c *Card) stepPane(dir int) {
	n := len(c.panes)
	if n == 0 {
		return
	}
	i := slices.IndexFunc(c.panes, func(a paneAt) bool { return a.p == c.pane })
	c.pane = c.panes[((i+dir)%n+n)%n].p
}

// zoom is F8 for pane p: p maximized in the Split holding it, or that Split restored.
func (c *Card) zoom(p *Pane) {
	s, side := parentOf(&Pane{Split: c.root}, p)
	if s == nil {
		return
	}
	if s.zoom == zoomNone {
		s.zoom = side
	} else {
		s.zoom = zoomNone
	}
}

// parentOf is the Split holding p as one of its sides, under root, and which side; nil when none.
func parentOf(root, p *Pane) (*Split, int) {
	s := root.Split
	if s == nil {
		return nil, zoomNone
	}
	switch p {
	case &s.A:
		return s, zoomA
	case &s.B:
		return s, zoomB
	}
	if ps, side := parentOf(&s.A, p); ps != nil {
		return ps, side
	}
	return parentOf(&s.B, p)
}

// paneKey is Key for a card of panes: F7, Shift+F7 and F8 are the panes'; every other key is the
// focused pane's page's, as a one-pane card handles it.
func (c *Card) paneKey(k plugin.Key) bool {
	if !k.Ctrl && !k.Alt {
		switch {
		case k.Key == "F7":
			c.stepPane(map[bool]int{false: 1, true: -1}[k.Shift])
			c.Draw()
			return true
		case k.Key == "F8" && !k.Shift && c.pane != nil:
			c.zoom(c.pane)
			c.Draw()
			return true
		}
	}
	if c.pane == nil {
		return false
	}
	if !c.leafOf(c.pane).card.Key(k) {
		return false
	}
	c.Draw()
	return true
}

// panePointer is Pointer for a card of panes.
func (c *Card) panePointer(at plugin.Pointer) bool {
	if c.drag != nil && at.Button == "left" {
		switch at.Kind {
		case "drag", "release":
			c.moveDivider(c.drag.s, at)
			if at.Kind == "release" {
				c.drag = nil
			}
			c.Draw()
			return true
		case "cancel":
			c.drag = nil // where it is
			return true
		}
	}
	if at.Kind != "press" && at.Kind != "wheel" {
		return false // a drag, release or cancel with no divider drag in hand
	}
	if at.X < 0 || at.X >= c.w || at.Y < 0 || at.Y >= c.h || at.Count < 1 {
		return false
	}
	if at.Kind == "press" && at.Button == "left" {
		for _, d := range c.dividers {
			if at.X >= d.x && at.X < d.x+d.w && at.Y >= d.y && at.Y < d.y+d.h {
				c.drag = &dragging{s: d.s}
				return true
			}
		}
	}
	for _, a := range c.panes {
		if at.X < a.x || at.X >= a.x+a.w {
			continue
		}
		if at.Y == a.title {
			if at.Kind != "press" || at.Button != "left" {
				return false
			}
			c.pane = a.p
			if at.Count == 2 {
				c.zoom(a.p)
			}
			c.Draw()
			return true
		}
		if at.Y < a.y || at.Y >= a.y+a.h {
			continue
		}
		local := at
		local.X, local.Y = at.X-a.x, at.Y-a.y
		if at.Kind == "wheel" {
			if !c.leafOf(a.p).card.Pointer(local) {
				return false
			}
			c.Draw()
			return true
		}
		if at.Button != "left" {
			return false
		}
		c.pane = a.p
		c.leafOf(a.p).card.Pointer(local)
		c.Draw()
		return true
	}
	return false
}

// moveDivider puts s's divider where at is along its axis, kept within its sides' least sizes: the
// event's coordinates are never clamped, so a release off the view still ends the drag, at the
// nearest place the divider may be.
//
// Ratio stays inside (0, 1) whatever the size: share reads 0 and 1 or more as a half, so a divider
// put at either end of a card too small for both panes (all of it A's) must not store 1, or the
// card's next, larger size would put it in the middle. A quarter cell in from each end is a ratio
// share rounds back to the same cell, at this size, and the end it means at a larger one.
func (c *Card) moveDivider(s *Split, at plugin.Pointer) {
	i := slices.IndexFunc(c.dividers, func(d divider) bool { return d.s == s })
	if i < 0 {
		return
	}
	d := c.dividers[i]
	pos := at.X
	if s.Vertical {
		pos = at.Y
	}
	avail := d.n - 1
	if avail <= 0 {
		return
	}
	a := max(min(pos-d.from, avail-d.minB), min(d.minA, avail))
	edge := 0.25 / float64(avail)
	s.Ratio = min(max(float64(a)/float64(avail), edge), 1-edge)
}
