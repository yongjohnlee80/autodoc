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

// pluginView is the dialog's inside: the plugin's latest frame, and its keys.
type pluginView struct {
	widget.Base
	run          *pluginRun
	w, h         int  // the size the manifest asks for
	fill         bool // the height is the float's share of the screen ("80%"): take all of it
	sentW, sentH int  // the size last told to the plugin: plugin.open's, then each resize's
	laidW, laidH int
}

var _ tuicore.Focusable = (*pluginView)(nil)

func (v *pluginView) AcceptsFocus() bool { return true }

// Layout is the manifest's size, within the screen's; the plugin is told a size it did not ask for.
func (v *pluginView) Layout(c tuicore.Constraints) tuicore.Size {
	w := min(max(v.w, c.MinW), c.MaxW)
	h := min(max(v.h, c.MinH), c.MaxH)
	if v.fill {
		h = c.MaxH
	}
	v.laidW, v.laidH = w, h
	if ctx := v.Context(); ctx != nil && (!v.run.opened || w != v.sentW || h != v.sentH) {
		ctx.AfterLayout("plugin-resize", v.resized) // Layout itself sends nothing
	}
	return tuicore.Size{W: w, H: h}
}

// resized follows a layout: the first opens the plugin at the laid size, a later one that changed
// it is plugin.resize.
func (v *pluginView) resized() {
	if !v.run.opened {
		v.sentW, v.sentH = v.laidW, v.laidH
		v.run.openAt(v.laidW, v.laidH)
		if v.run.focusOnOpen {
			v.run.focusOnOpen = false
			v.RequestFocus()
		}
		return
	}
	if v.laidW == v.sentW && v.laidH == v.sentH {
		return
	}
	v.sentW, v.sentH = v.laidW, v.laidH
	v.run.send(plugin.MethodResize, plugin.ResizeParams(v.laidW, v.laidH))
}

func (v *pluginView) Render(s tuicore.Surface) {
	sz := s.Size()
	base := style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)
	s.Fill(tuicore.Rect{W: sz.W, H: sz.H}, " ", base)
	v.run.mu.Lock()
	frame := v.run.frame
	v.run.mu.Unlock()
	for y, row := range frame {
		if y >= sz.H {
			break
		}
		x := 0
		for _, c := range row {
			for _, ch := range c.text {
				if x >= sz.W {
					break
				}
				g := string(ch)
				s.SetCell(x, y, g, c.st)
				x += max(s.StringWidth(g), 1)
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

// HandleEvent: Esc closes a dialog, or gives a card's keys back to the editor; every other key is
// the plugin's. A card is told when it takes the keys (a click, or its command) and gives them back.
func (v *pluginView) HandleEvent(ev tuicore.Event) bool {
	if f, ok := ev.(tuicore.FocusEvent); ok {
		if !f.Terminal && !v.run.e.m.modal() && f.Gained != v.run.focused && !v.run.closing {
			v.run.focused = f.Gained
			v.run.send(plugin.MethodFocus, plugin.FocusParams(f.Gained))
		}
		return false
	}
	e, ok := ev.(tuicore.KeyEvent)
	if !ok || e.Kind == tuicore.KeyRelease {
		return false
	}
	if e.Code == tuicore.KeyEscape && e.Mods.Chord() == 0 {
		if !v.run.e.m.modal() {
			// the card stays open; the editor has the keys again (ADR 1791268009 §2.2)
			v.run.h.keep(v.run.h.p.Call("editor", "forceActiveFocus"))
			return true
		}
		if v.run.e.m.Dialog.Esc == "hide" {
			v.run.hide()
		} else {
			v.run.close("")
		}
		return true
	}
	if k, ok := pluginKey(e); ok {
		v.run.send(plugin.MethodKey, plugin.KeyParams(k))
	}
	return true
}

var pluginKeyNames = map[rune]string{
	tuicore.KeyUp: "Up", tuicore.KeyDown: "Down", tuicore.KeyLeft: "Left", tuicore.KeyRight: "Right",
	tuicore.KeyEnter: "Enter", tuicore.KeyTab: "Tab", tuicore.KeyBackspace: "Backspace",
	tuicore.KeyDelete: "Delete", tuicore.KeyInsert: "Insert", tuicore.KeyHome: "Home", tuicore.KeyEnd: "End",
	tuicore.KeyPageUp: "PageUp", tuicore.KeyPageDown: "PageDown",
	tuicore.KeyF1: "F1", tuicore.KeyF2: "F2", tuicore.KeyF3: "F3", tuicore.KeyF4: "F4", tuicore.KeyF5: "F5",
	tuicore.KeyF6: "F6", tuicore.KeyF7: "F7", tuicore.KeyF8: "F8", tuicore.KeyF9: "F9", tuicore.KeyF10: "F10",
	tuicore.KeyF11: "F11", tuicore.KeyF12: "F12",
}

// pluginKey is a key as the protocol names it.
func pluginKey(e tuicore.KeyEvent) (plugin.Key, bool) {
	k := plugin.Key{Ctrl: e.Mods&tuicore.ModCtrl != 0, Alt: e.Mods&tuicore.ModAlt != 0, Shift: e.Mods&tuicore.ModShift != 0}
	switch {
	case pluginKeyNames[e.Code] != "":
		k.Key = pluginKeyNames[e.Code]
	case e.Text != "":
		k.Key, k.Text = e.Text, e.Text
	case e.Code >= ' ' && e.Code < tuicore.KeyInsert && utf8.ValidRune(e.Code):
		k.Key = string(e.Code) // a chord, Ctrl+a: no text
	default:
		return plugin.Key{}, false
	}
	return k, true
}
