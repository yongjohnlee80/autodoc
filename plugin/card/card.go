package card

import (
	"fmt"
	"slices"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/golib/tui/style"
)

// Options are a Card's settings.
type Options struct {
	Modal bool // a modal dialog: always focused, since AutoDoc sends it no plugin.focus
}

// Host is what a Card draws to: *plugin.Peer, or a test's fake.
type Host interface {
	Frame(*plugin.Frame) error
	Title(string) error
}

// Card is a plugin's card or dialog: its size, theme and focus, the page it shows, and the
// control with the focus. Its methods run on the Handler's goroutine only.
type Card struct {
	page    func() Page
	o       Options
	host    Host
	w, h    int
	theme   plugin.Theme
	focused bool // the card has AutoDoc's keys: active vs inactive selection colours

	focus  Control // the control with the focus; nil when the page has none
	from   int     // the body's first row in view
	titled string  // the title last sent
	hits   []hit   // what each frame row of the last draw holds, for Pointer
}

// New is a Card showing page(), asked for afresh on every draw.
func New(page func() Page, o Options) *Card {
	return &Card{page: page, o: o, focused: o.Modal}
}

// Open is plugin.open: the card draws to p at the size and in the theme given.
func (c *Card) Open(p *plugin.Peer, o plugin.Open) { c.Attach(p, o.Width, o.Height, o.Theme) }

// Attach is Open with any Host: a test's, say.
func (c *Card) Attach(h Host, w, hh int, t plugin.Theme) {
	c.host, c.w, c.h, c.theme = h, w, hh, t
	c.Draw()
}

// Resize is plugin.resize.
func (c *Card) Resize(w, h int) { c.w, c.h = w, h; c.Draw() }

// Theme is plugin.theme.
func (c *Card) Theme(t plugin.Theme) { c.theme = t; c.Draw() }

// Focus is plugin.focus: the card took AutoDoc's keys, or gave them back. A modal one ignores it.
func (c *Card) Focus(focused bool) {
	c.focused = focused || c.o.Modal
	c.Draw()
}

// Focused is the control with the focus; nil when the page has none.
func (c *Card) Focused() Control { return c.focus }

// FocusOn gives ctl the focus, and draws.
func (c *Card) FocusOn(ctl Control) { c.focus = ctl; c.Draw() }

// Typing reports whether an Input has the focus: every printable key is then text, q too.
func (c *Card) Typing() bool {
	_, ok := c.focus.(*Input)
	return ok
}

// controls are the page's controls in Tab order: the header's, the body's, then the buttons.
func controls(p Page) []Control {
	var out []Control
	for _, bs := range [][]Block{p.Header, p.Body} {
		for _, b := range bs {
			if ctl, ok := b.(Control); ok {
				out = append(out, ctl)
			}
		}
	}
	if p.Buttons != nil {
		out = append(out, p.Buttons)
	}
	return out
}

// settle keeps the focus on a control of ctls: the first, when the focused one left the page.
func (c *Card) settle(ctls []Control) {
	if c.focus != nil && slices.Contains(ctls, c.focus) {
		return
	}
	c.focus = nil
	if len(ctls) > 0 {
		c.focus = ctls[0]
	}
}

// Draw sends the card as it stands: the page laid out to the size AutoDoc last gave, and the
// title when it has changed.
func (c *Card) Draw() {
	if c.host == nil {
		return
	}
	p := c.page()
	c.settle(controls(p))
	if p.Title != "" && p.Title != c.titled {
		if c.host.Title(p.Title) == nil {
			c.titled = p.Title
		}
	}
	if c.w <= 0 || c.h <= 0 {
		return
	}
	_ = c.host.Frame(c.render(p))
}

// active reports whether ctl's selection wears the active colours: the card and the control
// both have the focus.
func (c *Card) active(ctl Control) bool { return c.focused && c.focus == ctl }

// The theme's roles for a selection: the document's highlight, as AutoDoc's results table uses,
// and the inactive one while the card or the control lacks the focus.
const (
	roleSelBG         = "document.highlight"
	roleSelFG         = "document.highlightedText"
	roleInactiveSelBG = "app.inactive.highlight"
	roleInactiveSelFG = "app.inactive.highlightedText"
)

// selection is the selected row's and the focused button's style: the theme's highlight, its text
// made legible against it (4.5:1), or Bold when the theme names either role not.
func (c *Card) selection(active bool) plugin.Style {
	bgKey, fgKey := roleSelBG, roleSelFG
	if !active {
		bgKey, fgKey = roleInactiveSelBG, roleInactiveSelFG
	}
	bg, fg := c.theme.Colors[bgKey], c.theme.Colors[fgKey]
	if bg == "" || fg == "" {
		return plugin.Style{Bold: true}
	}
	if bgColor, err := style.ParseColor(bg); err == nil {
		if fgColor, err := style.ParseColor(fg); err == nil {
			if r, g, b, ok := style.Legible(fgColor, bgColor, 4.5).RGBValues(); ok {
				fg = fmt.Sprintf("#%02x%02x%02x", r, g, b)
			}
		}
	}
	return plugin.Style{FG: fg, BG: bg}
}
