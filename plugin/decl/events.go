package decl

import (
	"unicode/utf8"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/golib/tui"
)

// keyEvent is k as a terminal would have sent it: a named key by the code plugin.KeyCode gives it
// back (the table the host named it with), a printable key by its first rune and its text, a chord
// (Ctrl+a) by its rune alone. Ctrl, Alt and Shift are the event's modifiers.
func keyEvent(k plugin.Key) tui.KeyEvent {
	e := tui.KeyEvent{Kind: tui.KeyPress}
	if k.Ctrl {
		e.Mods |= tui.ModCtrl
	}
	if k.Alt {
		e.Mods |= tui.ModAlt
	}
	if k.Shift {
		e.Mods |= tui.ModShift
	}
	if code, ok := plugin.KeyCode(k.Key); ok {
		e.Code = code
		return e
	}
	if k.Text != "" {
		e.Code, _ = utf8.DecodeRuneInString(k.Text)
		e.Text = k.Text
		return e
	}
	e.Code, _ = utf8.DecodeRuneInString(k.Key)
	return e
}

// mouseEvents are at as the App reads the pointer: a left press is a press and its release (the
// host sends no release); a wheel step is Count wheel events.
func mouseEvents(at plugin.Pointer) []tui.Event {
	switch {
	case at.Kind == "press" && at.Button == "left":
		return []tui.Event{
			tui.MouseEvent{Kind: tui.MousePress, Button: tui.MouseLeft, X: at.X, Y: at.Y},
			tui.MouseEvent{Kind: tui.MouseRelease, Button: tui.MouseLeft, X: at.X, Y: at.Y},
		}
	case at.Kind == "wheel" && (at.Button == "up" || at.Button == "down"):
		b := tui.WheelDown
		if at.Button == "up" {
			b = tui.WheelUp
		}
		out := make([]tui.Event, 0, max(at.Count, 1))
		for range max(at.Count, 1) {
			out = append(out, tui.MouseEvent{Kind: tui.MouseWheel, Button: b, X: at.X, Y: at.Y})
		}
		return out
	}
	return nil
}
