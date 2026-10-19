package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/yongjohnlee80/autodoc/plugin"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/style"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE PLUGIN'S VIEW — a dialog plugin's latest frame, drawn in its dialog, and the keys passed to
// it (plugins.go).

type pluginCell struct {
	text string
	st   style.Style
}

// resolveFrame reads a frame's colours: an unknown one is the default, and counted.
func resolveFrame(rows []plugin.Row) (out [][]pluginCell, bad int) {
	out = make([][]pluginCell, len(rows))
	for i, row := range rows {
		cells := make([]pluginCell, 0, len(row))
		for _, run := range row {
			st, n := runStyle(run.Style)
			bad += n
			cells = append(cells, pluginCell{text: run.Text, st: st})
		}
		out[i] = cells
	}
	return out, bad
}

// runStyle is a run's style over the dialog's own: an empty colour, or "default", leaves the
// dialog's.
func runStyle(s plugin.Style) (style.Style, int) {
	st := style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)
	bad := 0
	for _, c := range []struct {
		v  string
		fg bool
	}{{s.FG, true}, {s.BG, false}} {
		if c.v == "" || strings.EqualFold(strings.TrimSpace(c.v), "default") {
			continue
		}
		col, err := style.ParseColor(c.v)
		if err != nil {
			bad++
			continue
		}
		if c.fg {
			st = st.Foreground(col)
		} else {
			st = st.Background(col)
		}
	}
	return st.Bold(s.Bold).Italic(s.Italic).Underline(s.Underline), bad
}

// pluginView is the dialog's inside: the plugin's latest frame, and its keys. A modal dialog's is
// made with its run; a card's is its panel's PluginCard (pluginpanels.go), which shows each run of
// its plugin in turn, and nothing while none runs.
type pluginView struct {
	widget.Base
	run          *pluginRun // nil: a card whose plugin is not running
	pl           *plugins
	card         string // the card plugin whose panel this is; "" for a modal dialog's
	w, h         int    // the size the manifest asks for
	fill         bool   // the height is the float's share of the screen ("80%"): take all of it
	sentW, sentH int    // the size last told to the plugin: plugin.open's, then each resize's
	laidW, laidH int
	menu         *widget.EditorMenu // a card's right-click menu
}

var _ tuicore.Focusable = (*pluginView)(nil)

func (v *pluginView) AcceptsFocus() bool { return true }

// attach makes the view r's: a card's panel showing a run of its plugin.
func (v *pluginView) attach(r *pluginRun) {
	v.run, r.view = r, v
	v.sentW, v.sentH = 0, 0
	v.MarkDirty()
}

// detach leaves the view showing nothing, when it is r's.
func (v *pluginView) detach(r *pluginRun) {
	if v.run == r {
		v.run = nil
		v.MarkDirty()
	}
}

// Layout is the manifest's size, within the screen's; the plugin is told a size it did not ask for.
// A card fills its panel.
func (v *pluginView) Layout(c tuicore.Constraints) tuicore.Size {
	w := min(max(v.w, c.MinW), c.MaxW)
	h := min(max(v.h, c.MinH), c.MaxH)
	if v.fill || v.card != "" && c.MaxH != tuicore.Unbounded {
		h = c.MaxH
	}
	if v.card != "" && c.MaxW != tuicore.Unbounded {
		w = c.MaxW
	}
	v.laidW, v.laidH = w, h
	if ctx := v.Context(); ctx != nil && v.run != nil && (!v.run.opened || w != v.sentW || h != v.sentH) {
		ctx.AfterLayout("plugin-resize", v.resized) // Layout itself sends nothing
	}
	return tuicore.Size{W: w, H: h}
}

// resized follows a layout: the first opens the plugin at the laid size, a later one that changed
// it is plugin.resize.
func (v *pluginView) resized() {
	r := v.run
	if r == nil {
		return
	}
	if !r.opened {
		v.sentW, v.sentH = v.laidW, v.laidH
		r.openAt(v.laidW, v.laidH)
		if r.focusOnOpen {
			r.focusOnOpen = false
			v.RequestFocus()
		}
		return
	}
	if v.laidW == v.sentW && v.laidH == v.sentH {
		return
	}
	v.sentW, v.sentH = v.laidW, v.laidH
	r.send(plugin.MethodResize, plugin.ResizeParams(v.laidW, v.laidH))
}

func (v *pluginView) Render(s tuicore.Surface) {
	sz := s.Size()
	base := style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)
	s.Fill(tuicore.Rect{W: sz.W, H: sz.H}, " ", base)
	if v.run == nil {
		return
	}
	v.run.mu.Lock()
	frame := v.run.frame
	v.run.mu.Unlock()
	for y, row := range frame {
		if y >= sz.H {
			break
		}
		x := 0
		for _, c := range row {
			// a grapheme cluster at a time, as plugin.Frame placed it: an accent written as a
			// combining mark, a ZWJ emoji or a flag is one character, not several
			for g := range tuicore.Graphemes(c.text) {
				if x >= sz.W {
					break
				}
				s.SetCell(x, y, g, c.st)
				x += max(s.StringWidth(g), 1) // a lone zero-width cluster still takes a column
			}
		}
	}
}

// RequestFocus gives the card the keys: a card started by its command, or one already open.
func (v *pluginView) RequestFocus() {
	if ctx := v.Context(); ctx != nil {
		ctx.RequestFocus()
	}
}

// HandleEvent: Esc closes a dialog, or leaves a card's panel to close it (its Drawer's Esc); every
// other key is the plugin's, q included: a card's q is the plugin's to give, through host.close. A
// card is told when it takes the keys (a click, or its command) and gives them back, and a right
// press on it opens its menu.
func (v *pluginView) HandleEvent(ev tuicore.Event) bool {
	r := v.run
	if m, ok := ev.(tuicore.MouseEvent); ok {
		if v.card != "" && m.Kind == tuicore.MousePress && m.Button == tuicore.MouseRight && m.Mods.Chord() == 0 {
			return v.openMenu(tuicore.Point{X: m.X, Y: m.Y})
		}
		if r != nil && r.e.m.Protocol >= 6 && m.Mods.Chord() == 0 &&
			m.X >= 0 && m.X < v.laidW && m.Y >= 0 && m.Y < v.laidH {
			p := plugin.Pointer{X: m.X, Y: m.Y, Count: min(max(m.Count, 1), 16)}
			switch {
			case m.Kind == tuicore.MousePress && m.Button == tuicore.MouseLeft:
				p.Kind, p.Button = "press", "left"
			case m.Kind == tuicore.MouseWheel && m.Button == tuicore.WheelUp:
				p.Kind, p.Button = "wheel", "up"
			case m.Kind == tuicore.MouseWheel && m.Button == tuicore.WheelDown:
				p.Kind, p.Button = "wheel", "down"
			}
			if p.Kind != "" {
				r.send(plugin.MethodPointer, plugin.PointerParams(p))
				return true
			}
		}
		return false
	}
	if r == nil {
		return false
	}
	if f, ok := ev.(tuicore.FocusEvent); ok {
		if !f.Terminal && !r.e.m.Modal() && f.Gained != r.focused && !r.closing {
			r.focused = f.Gained
			r.send(plugin.MethodFocus, plugin.FocusParams(f.Gained))
		}
		return false
	}
	e, ok := ev.(tuicore.KeyEvent)
	if !ok || e.Kind == tuicore.KeyRelease {
		return false
	}
	if e.Code == tuicore.KeyEscape && e.Mods.Chord() == 0 {
		if !r.e.m.Modal() {
			return false // the panel's Drawer closes on it: the card hides, its plugin runs on
		}
		if r.e.m.Dialog.Esc == "hide" {
			r.hide()
		} else {
			r.close("")
		}
		return true
	}
	if k, ok := pluginKey(e); ok {
		r.send(plugin.MethodKey, plugin.KeyParams(k))
	}
	return true
}

// openMenu is a card's right-click menu, at a point in the card: Close (Esc), which closes its
// panel as Esc does.
func (v *pluginView) openMenu(at tuicore.Point) bool {
	if v.menu == nil {
		v.menu = widget.NewEditorMenu(v, nil)
		v.menu.SetEnabled(true)
		v.menu.SetRows(func() []widget.MenuItemModel {
			return []widget.MenuItemModel{menuCommand("plugin.card.close", "&Close (Esc)",
				widget.CoreMenuAction{ID: "plugin.card.close", Run: func(*widget.EditorCore) { v.closePanel() }})}
		})
	}
	return v.menu.OpenAt(at)
}

// closePanel closes the card's panel: the card hides, its plugin runs on.
func (v *pluginView) closePanel() {
	if v.pl == nil || v.card == "" {
		return
	}
	id := cardPanelID(v.card)
	if v.pl.c.PanelOpen(id) {
		v.pl.c.Keep(v.pl.c.View(id, "close"))
	}
}

// pluginKey is a key as the protocol names it.
func pluginKey(e tuicore.KeyEvent) (plugin.Key, bool) {
	k := plugin.Key{Ctrl: e.Mods&tuicore.ModCtrl != 0, Alt: e.Mods&tuicore.ModAlt != 0, Shift: e.Mods&tuicore.ModShift != 0}
	switch {
	case plugin.KeyName(e.Code) != "":
		k.Key = plugin.KeyName(e.Code)
	case e.Text != "":
		k.Key, k.Text = e.Text, e.Text
	case e.Code >= ' ' && e.Code < tuicore.KeyInsert && utf8.ValidRune(e.Code):
		k.Key = string(e.Code) // a chord, Ctrl+a: no text
	default:
		return plugin.Key{}, false
	}
	return k, true
}
