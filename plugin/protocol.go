// Package plugin is AutoDoc's plugins (ADR 0209): the protocol between the TUI and a plugin it
// runs, and the SDK a plugin is written with.
//
// A dialog plugin is a program the TUI starts when its Plugins menu entry is chosen. The TUI draws a
// dialog over the page and fills it with the plugin's frames; every key but Esc goes to the plugin,
// and Esc closes it. The two speak msgpack-RPC NOTIFICATIONS, both ways, over the plugin's stdin and
// stdout — nothing waits for an answer:
//
//	host → plugin   plugin.open {protocol, width, height, theme}   the first, once
//	                plugin.key {key, text, ctrl, alt, shift}
//	                plugin.resize {width, height}
//	                plugin.theme {theme}
//	                plugin.hide {}, plugin.show {}                  esc = "hide": hidden, shown again
//	                plugin.close {}                                 the last
//	plugin → host   host.ready {protocol}                           the answer to plugin.open
//	                host.frame {rows}                               the whole dialog, any time
//	                host.title {title}
//	                host.close {}
//
// A plugin in Go writes a [Handler] and calls [Serve]; one in another language speaks the table
// above. The frame's colours are the themes' vocabulary: "default", an ANSI name ("brightwhite"), or
// "#rrggbb" — golib's style.ParseColor reads them, as it reads the theme the plugin is sent.
package plugin

import (
	"errors"
	"fmt"
)

// Protocol is the plugin protocol's version. The host and the plugin must speak the same one: any
// change to the notifications or their parameters bumps it.
const Protocol = 1

// The notifications the host sends.
const (
	MethodOpen   = "plugin.open"
	MethodKey    = "plugin.key"
	MethodResize = "plugin.resize"
	MethodTheme  = "plugin.theme"
	MethodClose  = "plugin.close"
	// MethodHide and MethodShow are a dialog whose manifest says esc = "hide": Esc hid it, and
	// its Plugins menu entry showed it again. A plugin may ignore them; it keeps running either way.
	MethodHide = "plugin.hide"
	MethodShow = "plugin.show"
)

// The notifications the plugin sends.
const (
	MethodReady     = "host.ready"
	MethodFrame     = "host.frame"
	MethodTitle     = "host.title"
	MethodHostClose = "host.close"
)

// Limits on what the host reads of a frame: past them it is clipped.
const (
	MaxRows       = 500
	MaxRunsPerRow = 1000
)

// Theme is the theme the TUI wears: its name, and its colours by their dotted names
// ("app.window": "white", "document.cursor": "#ffaf00").
type Theme struct {
	Name   string
	Colors map[string]string
}

// Open is plugin.open: the protocol the host speaks, the dialog's size inside its border, and the
// theme.
type Open struct {
	Protocol      int
	Width, Height int
	Theme         Theme
}

// Key is a key typed into the dialog. Key is its text for a printable key (" " for Space), or one of
// Up, Down, Left, Right, Enter, Tab, Backspace, Delete, Insert, Home, End, PageUp, PageDown, F1 to
// F12. Esc is never sent: it closes the dialog.
type Key struct {
	Key, Text        string
	Ctrl, Alt, Shift bool
}

// Style is how a run of a frame looks: its colours, in the themes' vocabulary ("" is "default"),
// and its attributes.
type Style struct {
	FG, BG                  string
	Bold, Italic, Underline bool
}

// Run is text in one style.
type Run struct {
	Text string
	Style
}

// Row is a frame's row: its runs, left to right.
type Row []Run

// ---------------------------------------------------------------- the wire

// Each notification carries one map. The msgpack encoder takes map[string]any and []any, and the
// decoder gives them back with numbers as int64 or uint64, so these convert by hand.

func (t Theme) wire() map[string]any {
	colors := make(map[string]any, len(t.Colors))
	for k, v := range t.Colors {
		colors[k] = v
	}
	return map[string]any{"name": t.Name, "colors": colors}
}

// OpenParams are plugin.open's parameters.
func OpenParams(o Open) []any {
	return one(map[string]any{"protocol": o.Protocol, "width": o.Width, "height": o.Height, "theme": o.Theme.wire()})
}

// KeyParams are plugin.key's.
func KeyParams(k Key) []any {
	return one(map[string]any{"key": k.Key, "text": k.Text, "ctrl": k.Ctrl, "alt": k.Alt, "shift": k.Shift})
}

// ResizeParams are plugin.resize's.
func ResizeParams(w, h int) []any { return one(map[string]any{"width": w, "height": h}) }

// ThemeParams are plugin.theme's.
func ThemeParams(t Theme) []any { return one(map[string]any{"theme": t.wire()}) }

// ReadyParams are host.ready's.
func ReadyParams(protocol int) []any { return one(map[string]any{"protocol": protocol}) }

// TitleParams are host.title's.
func TitleParams(title string) []any { return one(map[string]any{"title": title}) }

// EmptyParams are plugin.close's and host.close's.
func EmptyParams() []any { return one(map[string]any{}) }

// FrameParams are host.frame's. A run's style keys are left out when they are their zero value.
func FrameParams(rows []Row) []any {
	out := make([]any, len(rows))
	for i, row := range rows {
		runs := make([]any, len(row))
		for j, r := range row {
			m := map[string]any{"t": r.Text}
			if r.FG != "" {
				m["fg"] = r.FG
			}
			if r.BG != "" {
				m["bg"] = r.BG
			}
			if r.Bold {
				m["b"] = true
			}
			if r.Italic {
				m["i"] = true
			}
			if r.Underline {
				m["u"] = true
			}
			runs[j] = m
		}
		out[i] = runs
	}
	return one(map[string]any{"rows": out})
}

func one(m map[string]any) []any { return []any{m} }

// ErrParams is a notification whose parameters are not the protocol's.
var ErrParams = errors.New("plugin: malformed parameters")

// arg is a notification's one map.
func arg(params []any) (map[string]any, error) {
	if len(params) != 1 {
		return nil, fmt.Errorf("%w: want one map, got %d parameters", ErrParams, len(params))
	}
	m, ok := params[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: want a map, got %T", ErrParams, params[0])
	}
	return m, nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func flag(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

func num(m map[string]any, k string) (int, bool) {
	switch n := m[k].(type) {
	case int64:
		return int(n), true
	case uint64:
		return int(n), n <= 1<<31
	case int:
		return n, true
	}
	return 0, false
}

func readTheme(v any) Theme {
	m, _ := v.(map[string]any)
	t := Theme{Name: str(m, "name"), Colors: map[string]string{}}
	colors, _ := m["colors"].(map[string]any)
	for k, c := range colors {
		if s, ok := c.(string); ok {
			t.Colors[k] = s
		}
	}
	return t
}

// ReadOpen reads plugin.open.
func ReadOpen(params []any) (Open, error) {
	m, err := arg(params)
	if err != nil {
		return Open{}, err
	}
	p, ok1 := num(m, "protocol")
	w, ok2 := num(m, "width")
	h, ok3 := num(m, "height")
	if !ok1 || !ok2 || !ok3 {
		return Open{}, fmt.Errorf("%w: plugin.open wants protocol, width and height", ErrParams)
	}
	return Open{Protocol: p, Width: w, Height: h, Theme: readTheme(m["theme"])}, nil
}

// ReadKey reads plugin.key.
func ReadKey(params []any) (Key, error) {
	m, err := arg(params)
	if err != nil {
		return Key{}, err
	}
	return Key{Key: str(m, "key"), Text: str(m, "text"), Ctrl: flag(m, "ctrl"), Alt: flag(m, "alt"), Shift: flag(m, "shift")}, nil
}

// ReadResize reads plugin.resize.
func ReadResize(params []any) (w, h int, err error) {
	m, err := arg(params)
	if err != nil {
		return 0, 0, err
	}
	w, ok1 := num(m, "width")
	h, ok2 := num(m, "height")
	if !ok1 || !ok2 {
		return 0, 0, fmt.Errorf("%w: plugin.resize wants width and height", ErrParams)
	}
	return w, h, nil
}

// ReadTheme reads plugin.theme.
func ReadTheme(params []any) (Theme, error) {
	m, err := arg(params)
	if err != nil {
		return Theme{}, err
	}
	return readTheme(m["theme"]), nil
}

// ReadReady reads host.ready: the plugin's protocol.
func ReadReady(params []any) (int, error) {
	m, err := arg(params)
	if err != nil {
		return 0, err
	}
	p, ok := num(m, "protocol")
	if !ok {
		return 0, fmt.Errorf("%w: host.ready wants protocol", ErrParams)
	}
	return p, nil
}

// ReadTitle reads host.title.
func ReadTitle(params []any) (string, error) {
	m, err := arg(params)
	if err != nil {
		return "", err
	}
	return str(m, "title"), nil
}

// ReadFrame reads host.frame. Rows past MaxRows and runs past MaxRunsPerRow are clipped; a run that
// is not a map with a string "t" is dropped, and dropped counts them, so the host can say so.
func ReadFrame(params []any) (rows []Row, dropped int, err error) {
	m, err := arg(params)
	if err != nil {
		return nil, 0, err
	}
	raw, ok := m["rows"].([]any)
	if !ok {
		return nil, 0, fmt.Errorf("%w: host.frame wants rows", ErrParams)
	}
	raw = raw[:min(len(raw), MaxRows)]
	rows = make([]Row, len(raw))
	for i, r := range raw {
		runs, ok := r.([]any)
		if !ok {
			dropped++
			continue
		}
		runs = runs[:min(len(runs), MaxRunsPerRow)]
		row := make(Row, 0, len(runs))
		for _, v := range runs {
			rm, ok := v.(map[string]any)
			t, isText := rm["t"].(string)
			if !ok || !isText {
				dropped++
				continue
			}
			row = append(row, Run{Text: t, Style: Style{FG: str(rm, "fg"), BG: str(rm, "bg"),
				Bold: flag(rm, "b"), Italic: flag(rm, "i"), Underline: flag(rm, "u")}})
		}
		rows[i] = row
	}
	return rows, dropped, nil
}
