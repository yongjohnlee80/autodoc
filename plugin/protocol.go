// Package plugin is AutoDoc's plugins (ADR 0209, ADR 1791268009): the protocol between the TUI and
// a plugin it runs, and the SDK a plugin is written with.
//
// A dialog plugin is a program the TUI starts when its Plugins menu entry is chosen. The TUI draws a
// dialog over the page and fills it with the plugin's frames; every key but Esc goes to the plugin,
// and Esc closes it. A non-modal one (a card) sits beside the page and takes the keys only when
// focused; a service has no surface at all. The two speak msgpack-RPC NOTIFICATIONS, both ways, over
// the plugin's stdin and stdout — nothing waits for an answer:
//
//	host → plugin   plugin.open {protocol, width, height, theme}   the first, once; a service's has no size
//	                plugin.key {key, text, ctrl, alt, shift}
//	                plugin.resize {width, height}
//	                plugin.theme {theme}
//	                plugin.hide {}, plugin.show {}                  esc = "hide": hidden, shown again
//	                plugin.focus {focused}                          2: a card took or gave back the keys
//	                plugin.document {path, workspace, text,         2: the open note, as [feed] declares
//	                  cursor, selection, version, too_large}
//	                plugin.command {id}                             2: one of the manifest's [[commands]]
//	                plugin.close {}                                 the last
//	plugin → host   host.ready {protocol}                           the answer to plugin.open
//	                host.frame {rows}                               the whole dialog, any time
//	                host.title {title}
//	                host.close {}
//
// The notifications marked 2 are protocol 2's. A host sends them only to a plugin whose manifest
// says protocol = 2 and declares them, so a protocol-1 plugin never sees one.
//
// A plugin in Go writes a [Handler] and calls [Serve]; one in another language speaks the table
// above. The frame's colours are the themes' vocabulary: "default", an ANSI name ("brightwhite"), or
// "#rrggbb" — golib's style.ParseColor reads them, as it reads the theme the plugin is sent.
package plugin

import (
	"errors"
	"fmt"
)

// The plugin protocol's versions this SDK speaks. A host speaks to each plugin the one its manifest
// says, and Serve answers any from MinProtocol to Protocol with that same one. The protocol rises
// when a manifest can declare something an older host must not run without, for a changed meaning,
// or for something a plugin must handle (ADR 1791268009 §1).
const (
	MinProtocol = 1
	Protocol    = 2
)

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
	// MethodFocus is a non-modal card focused (a click, or its command's key) or given back (Esc).
	MethodFocus = "plugin.focus"
	// MethodDocument is the open note, to a plugin whose manifest declares [feed] document = true.
	MethodDocument = "plugin.document"
	// MethodCommand is one of the manifest's [[commands]], chosen.
	MethodCommand = "plugin.command"
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
// theme. A service has no dialog: its size is 0 by 0.
type Open struct {
	Protocol      int
	Width, Height int
	Theme         Theme
}

// Position is a place in a note: its line and column, both from 1. A column counts characters as
// the editor's cursor does: grapheme clusters (what shows as one character, "é" written as e and a
// combining accent, say), not bytes or code points.
type Position struct{ Line, Col int }

// Range is a selection, from Start to End.
type Range struct{ Start, End Position }

// Document is plugin.document: the note open in the editor, as it stands, saved or not. Path is ""
// when none is open. TooLarge says the note was too large to send: Path, Workspace and Version are
// set, and the rest is empty. Version rises with every edit, so a plugin can drop a stale answer.
type Document struct {
	Path, Workspace string
	Text            string
	Cursor          Position
	Selection       []Range
	Version         int
	TooLarge        bool
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

// OpenParams are plugin.open's parameters. A service's (a size of 0 by 0) has no width or height.
func OpenParams(o Open) []any {
	m := map[string]any{"protocol": o.Protocol, "theme": o.Theme.wire()}
	if o.Width != 0 || o.Height != 0 {
		m["width"], m["height"] = o.Width, o.Height
	}
	return one(m)
}

// FocusParams are plugin.focus's.
func FocusParams(focused bool) []any { return one(map[string]any{"focused": focused}) }

// CommandParams are plugin.command's.
func CommandParams(id string) []any { return one(map[string]any{"id": id}) }

func (p Position) wire() map[string]any { return map[string]any{"line": p.Line, "col": p.Col} }

// DocumentParams are plugin.document's. A TooLarge one carries its path, workspace and version
// alone.
func DocumentParams(d Document) []any {
	m := map[string]any{"path": d.Path, "workspace": d.Workspace, "version": d.Version}
	if d.TooLarge {
		m["too_large"] = true
		return one(m)
	}
	sel := make([]any, len(d.Selection))
	for i, r := range d.Selection {
		sel[i] = map[string]any{"start": r.Start.wire(), "end": r.End.wire()}
	}
	m["text"], m["cursor"], m["selection"] = d.Text, d.Cursor.wire(), sel
	return one(m)
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

// ReadOpen reads plugin.open. A service's has no size: both width and height are left out, and
// read as 0.
func ReadOpen(params []any) (Open, error) {
	m, err := arg(params)
	if err != nil {
		return Open{}, err
	}
	p, ok := num(m, "protocol")
	if !ok {
		return Open{}, fmt.Errorf("%w: plugin.open wants protocol", ErrParams)
	}
	_, hasW := m["width"]
	_, hasH := m["height"]
	if !hasW && !hasH {
		return Open{Protocol: p, Theme: readTheme(m["theme"])}, nil
	}
	w, ok1 := num(m, "width")
	h, ok2 := num(m, "height")
	if !ok1 || !ok2 {
		return Open{}, fmt.Errorf("%w: plugin.open wants a width and a height, or neither", ErrParams)
	}
	return Open{Protocol: p, Width: w, Height: h, Theme: readTheme(m["theme"])}, nil
}

// ReadFocus reads plugin.focus.
func ReadFocus(params []any) (bool, error) {
	m, err := arg(params)
	if err != nil {
		return false, err
	}
	f, ok := m["focused"].(bool)
	if !ok {
		return false, fmt.Errorf("%w: plugin.focus wants focused", ErrParams)
	}
	return f, nil
}

// ReadCommand reads plugin.command: the command's id.
func ReadCommand(params []any) (string, error) {
	m, err := arg(params)
	if err != nil {
		return "", err
	}
	id := str(m, "id")
	if id == "" {
		return "", fmt.Errorf("%w: plugin.command wants id", ErrParams)
	}
	return id, nil
}

func readPosition(v any) Position {
	m, _ := v.(map[string]any)
	l, _ := num(m, "line")
	c, _ := num(m, "col")
	return Position{Line: l, Col: c}
}

// ReadDocument reads plugin.document. A selection entry that is not a map is dropped.
func ReadDocument(params []any) (Document, error) {
	m, err := arg(params)
	if err != nil {
		return Document{}, err
	}
	v, ok := num(m, "version")
	if !ok {
		return Document{}, fmt.Errorf("%w: plugin.document wants version", ErrParams)
	}
	d := Document{Path: str(m, "path"), Workspace: str(m, "workspace"), Version: v, TooLarge: flag(m, "too_large")}
	if d.TooLarge {
		return d, nil
	}
	d.Text, d.Cursor = str(m, "text"), readPosition(m["cursor"])
	sel, _ := m["selection"].([]any)
	for _, r := range sel {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		d.Selection = append(d.Selection, Range{Start: readPosition(rm["start"]), End: readPosition(rm["end"])})
	}
	return d, nil
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
