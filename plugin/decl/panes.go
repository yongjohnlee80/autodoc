package decl

import (
	"fmt"
	"strings"

	"github.com/yongjohnlee80/golib/parse/qml"
	"github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget/core"
)

// PANES — a card's program in rows and columns of titled panes, with the native kit's keys
// (plugin/card's Split and Pane), over golib's Split:
//
//	Panes {                          // two panes; nest a Panes in a Pane for more
//	    orientation: Tui.Horizontal  // side by side (Tui.Vertical: one above the other)
//	    ratio: 0.4                   // the first pane's share
//	    Pane { title: "Instances"; ListView { … } }
//	    Pane { title: "Agent"; TextArea { … } }
//	}
//
//	F7, Shift+F7     the next or previous pane takes the keys, in reading order across every
//	                 nested Panes (the outermost Panes takes them)
//	F8               the focused pane maximized in its Panes, or restored (the innermost Panes
//	                 whose pane holds the focus takes it)
//	Tab, Shift+Tab   the focused pane's controls only: the keys never leave a pane
//	double press on a pane's title line   F8 for that pane
//
// A program that starts with nothing holding the keys starts with the first pane holding them.
// A Pane whose content holds nothing that takes the keyboard is itself the stop F7 reaches; so is
// one whose every control is disabled now, until one is enabled and Tab is pressed. No trapping
// FocusScope: it would also keep the pointer and the keys from the other pane.

// paneTypes are the QML types every card's program gets: Panes and Pane.
func paneTypes() []tuidecl.Type {
	return []tuidecl.Type{
		{Name: "Panes", Children: tuidecl.Exactly(2), Build: buildPanes, Ctor: []string{"orientation"},
			Setters: map[string]tuidecl.Setter{"ratio": tuidecl.NumberSetter((*panesNode).setRatio)}},
		{Name: "Pane", Children: tuidecl.Exactly(1), Build: buildPane,
			Setters: map[string]tuidecl.Setter{"title": tuidecl.StringSetter((*paneNode).setTitle)}},
	}
}

// paneMin is a pane's least size across its Panes: a title line, a row and a bottom border.
const paneMin = 3

// panesNode is a Panes: golib's Split of two Panes, or of a Pane and a nested Panes.
type panesNode struct {
	core.Base
	split   *core.Split
	a, b    tui.Component
	started bool // the first layout has asked for the start's keys (start)
}

func buildPanes(b tuidecl.Build) (tui.Component, []string, error) {
	o := core.Horizontal
	consumed, err := b.ReadProps(map[string]tuidecl.Field{"orientation": func(v qml.SpecValue) error {
		// Tui.Horizontal or Tui.Vertical, as Split's orientation takes them: the constant's name,
		// written qualified or not, in any case
		switch name := strings.TrimPrefix(v.Raw, "Tui."); {
		case strings.EqualFold(name, "Horizontal"):
			o = core.Horizontal
		case strings.EqualFold(name, "Vertical"):
			o = core.Vertical
		default:
			return fmt.Errorf("want Tui.Horizontal or Tui.Vertical, got %q (at %s)", v.Raw, v.Pos)
		}
		return nil
	}})
	if err != nil {
		return nil, nil, err
	}
	for i, c := range b.Children {
		switch c.(type) {
		case *paneNode, *panesNode:
		default:
			return nil, nil, fmt.Errorf("a Panes holds two Panes or Pane; child %d is neither (at %s)", i, b.Pos)
		}
	}
	n := &panesNode{a: b.Children[0], b: b.Children[1]}
	n.split = core.NewSplit(o, n.a, n.b, core.WithMinSizes(paneMin, paneMin))
	return n, consumed, nil
}

func (n *panesNode) Init(ctx *tui.Context) {
	n.Base.Init(ctx)
	ctx.Mount(n.split)
}

func (n *panesNode) Layout(c tui.Constraints) tui.Size {
	s := n.Context().LayoutChild(n.split, c)
	n.Context().PlaceChild(n.split, tui.Rect{W: s.W, H: s.H})
	if !n.started {
		n.started = true
		n.Context().AfterLayout("plugin.decl.panes.start", n.start)
	}
	return s
}

// start gives the first pane the keys when the program starts with nothing holding them, as the
// native kit's card starts with its first pane focused: a key then has a pane to reach, F7 and F8
// among them. The outermost Panes alone; a document that gave the keys elsewhere (focus: true)
// keeps them there.
func (n *panesNode) start() {
	ctx := n.Context()
	if ctx == nil || ctx.Ancestor(isPanes) != nil {
		return
	}
	if root := topOf(ctx); root != nil && ctx.FocusWithin(root) {
		return
	}
	if ps := n.panes(); len(ps) > 0 {
		ps[0].focus()
	}
}

// topOf is the root of the tree ctx's node is in: the last of its ancestors.
func topOf(ctx *tui.Context) tui.Component {
	var top tui.Component
	ctx.Ancestor(func(c tui.Component) bool { top = c; return false })
	return top
}

func (n *panesNode) Render(tui.Surface) {}

func (n *panesNode) setRatio(r float64) { n.split.SetRatio(r) }

// HandleEvent takes F7 and Shift+F7 (the outermost Panes alone) and F8 (the innermost whose pane
// holds the focus: the focused node's ancestors see a key innermost first).
func (n *panesNode) HandleEvent(ev tui.Event) bool {
	e, ok := ev.(tui.KeyEvent)
	if !ok || e.Kind != tui.KeyPress {
		return false
	}
	switch {
	case e.Code == tui.KeyF7 && (e.Mods == 0 || e.Mods == tui.ModShift):
		if n.Context().Ancestor(isPanes) != nil {
			return false // the outermost steps across every nested Panes
		}
		dir := 1
		if e.Mods == tui.ModShift {
			dir = -1
		}
		n.step(dir)
		return true
	case e.Code == tui.KeyF8 && e.Mods == 0:
		side := n.sideHolding()
		if side == core.PaneNone {
			return false
		}
		n.toggleZoom(side)
		return true
	}
	return false
}

func isPanes(c tui.Component) bool { _, ok := c.(*panesNode); return ok }

// panes are the Panes' leaf panes in reading order, nested ones included.
func (n *panesNode) panes() []*paneNode {
	var out []*paneNode
	for _, c := range []tui.Component{n.a, n.b} {
		switch c := c.(type) {
		case *panesNode:
			out = append(out, c.panes()...)
		case *paneNode:
			if inner, ok := c.child.(*panesNode); ok {
				out = append(out, inner.panes()...)
			} else {
				out = append(out, c)
			}
		}
	}
	return out
}

// step gives the keys to the pane dir away from the one holding them, round; with none holding
// them, the first (or the last).
func (n *panesNode) step(dir int) {
	ps := n.panes()
	if len(ps) == 0 {
		return
	}
	at := -1
	for i, p := range ps {
		if n.Context().FocusWithin(p) {
			at = i
		}
	}
	next := 0
	switch {
	case at >= 0:
		next = (at + dir + len(ps)) % len(ps)
	case dir < 0:
		next = len(ps) - 1
	}
	ps[next].focus()
}

// sideHolding is which of its two sides holds the focus: PaneNone for neither.
func (n *panesNode) sideHolding() core.SplitPane {
	switch ctx := n.Context(); {
	case ctx.FocusWithin(n.a):
		return core.PaneA
	case ctx.FocusWithin(n.b):
		return core.PaneB
	}
	return core.PaneNone
}

// toggleZoom maximizes side in the Split, or restores both when it is the maximized one.
func (n *panesNode) toggleZoom(side core.SplitPane) {
	if n.split.Zoomed() == side {
		n.split.Zoom(core.PaneNone)
		return
	}
	n.split.Zoom(side)
}

// paneNode is a Pane: a titled Frame around its one child.
type paneNode struct {
	core.Base
	frame *core.Box
	child tui.Component
	// self: the pane itself has the keys, its child taking none now (every control disabled when
	// F7 or a title press came); Tab gives them to the first control enabled since
	self bool
}

func buildPane(b tuidecl.Build) (tui.Component, []string, error) {
	p := &paneNode{child: b.Children[0]}
	p.frame = core.NewBox(p.child)
	return p, nil, nil
}

func (p *paneNode) Init(ctx *tui.Context) {
	p.Base.Init(ctx)
	ctx.Mount(p.frame)
}

func (p *paneNode) Layout(c tui.Constraints) tui.Size {
	s := p.Context().LayoutChild(p.frame, c)
	p.Context().PlaceChild(p.frame, tui.Rect{W: s.W, H: s.H})
	return s
}

func (p *paneNode) Render(tui.Surface) {}

func (p *paneNode) setTitle(s string) { p.frame.SetTitle(s) }

// AcceptsFocus: a pane is a stop of its own only while its child holds nothing that takes the
// keys, or while F7 or a title press gave it them with every control disabled.
func (p *paneNode) AcceptsFocus() bool {
	ctx := p.Context()
	return p.self || ctx != nil && !ctx.HoldsFocusable(p.child)
}

var _ tui.Focusable = (*paneNode)(nil)

// focus gives the pane the keys: its first control, else the pane itself.
func (p *paneNode) focus() {
	ctx := p.Context()
	if ctx.HoldsFocusable(p.child) && ctx.FocusInto(p.child) {
		p.self = false
		return
	}
	p.self = true
	ctx.RequestFocus()
}

// HandleEvent keeps Tab and Shift+Tab (the key map's FocusNext and FocusPrev) inside the pane, and
// takes a double press on its title line as F8 for it.
func (p *paneNode) HandleEvent(ev tui.Event) bool {
	ctx := p.Context()
	switch e := ev.(type) {
	case tui.KeyEvent:
		if e.Kind != tui.KeyPress {
			return false
		}
		if cmd, ok := ctx.KeyCommand(e, tui.FocusNext, tui.FocusPrev); ok {
			dir := 1
			if cmd.ID == tui.FocusPrev.ID {
				dir = -1
			}
			if ctx.FocusStepWithin(p.child, dir) {
				p.self = false
			}
			return true // the keys never leave the pane by Tab
		}
	case tui.MouseEvent:
		if e.Kind == tui.MousePress && e.Button == tui.MouseLeft && e.Count == 2 && e.Y == 0 {
			if n, ok := ctx.Ancestor(isPanes).(*panesNode); ok {
				switch p {
				case n.a:
					n.toggleZoom(core.PaneA)
				case n.b:
					n.toggleZoom(core.PaneB)
				}
			}
			p.focus()
			return true
		}
	}
	return false
}
