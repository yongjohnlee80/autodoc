// Package plugin is AutoDoc's plugins (ADR 0209, ADR 1791268009): the protocol between the TUI and
// a plugin it runs, and the SDK a plugin is written with.
//
// A dialog plugin is a program the TUI starts when its Plugins menu entry is chosen. The TUI draws a
// dialog over the page and fills it with the plugin's frames; every key but Esc goes to the plugin,
// and Esc closes it. A non-modal one (a card) sits beside the page and takes the keys only when
// focused; a service has no surface at all. The two speak msgpack-RPC NOTIFICATIONS, both ways, over
// the plugin's stdin and stdout — never a request, which golib's RPC client cannot take:
//
//	host → plugin   plugin.open {protocol, width, height, theme}   the first, once; a service's has no size
//	                plugin.key {key, text, ctrl, alt, shift}
//	                plugin.pointer {kind, button, x, y, count}      6: a frame's left press or wheel
//	                plugin.resize {width, height}
//	                plugin.theme {theme}
//	                plugin.hide {}, plugin.show {}                  esc = "hide": hidden, shown again
//	                plugin.focus {focused}                          2: a card took or gave back the keys
//	                plugin.document {path, workspace, text,         2: the open note, as [feed] declares
//	                  cursor, selection, version, too_large,
//	                  cursor_byte, selection_bytes}                 4: the byte offsets
//	                plugin.command {id}                             2: one of the manifest's [[commands]]
//	                plugin.command {id, document}                   4: with the note as it stood, under [feed]
//	                plugin.result {call, ok, error}                 3: the answer to host.call
//	                plugin.table.fetch {id, n}                      4: up to n more rows of the table
//	                plugin.table.close {id}                         4: the table closed, or replaced
//	                plugin.close {}                                 the last
//	plugin → host   host.ready {protocol}                           the answer to plugin.open
//	                host.frame {rows}                               the whole dialog, any time
//	                host.title {title}
//	                host.call {call, id, args}                      3: run a command of AutoDoc's catalog
//	                host.table {id, title, fields, rows, more}      4: a table of rows, as [table] declares
//	                host.table {…, kinds}                           5: with each cell's kind
//	                host.rows {id, rows, more}                      4: the next rows, answering a fetch
//	                host.rows {…, kinds}                            5: with each cell's kind
//	                host.table_end {id, error}                      4: the table's rows ended early
//	                host.close {}
//
// The notifications marked 2 are protocol 2's, those marked 3 protocol 3's, those marked 4 protocol
// 4's. A host sends them only to a plugin whose manifest says that protocol (or a later one) and
// declares them, so a protocol-1 plugin never sees one. The fields marked 5 are read only from a
// plugin whose manifest says protocol 5. Pointer notifications go only to protocol-6 plugins.
//
// A table is the host's to show: the plugin sends its fields and its first rows (host.table), and
// AutoDoc shows them in a results panel, asking for more as the user nears the end of those loaded
// (plugin.table.fetch), one request at a time until host.rows answers it. more says rows remain;
// host.table_end ends them early, with why. A newer host.table replaces the table, and the host
// says plugin.table.close for the one it replaced, as it does when the panel closes. Each table has
// an id of its own: one that reuses the shown table's id is refused, and host.rows no fetch asked
// for is dropped.
//
// From protocol 5 a table's cells may carry their kinds: kinds is one string per row, a byte per
// field, each one of KindText, KindNull, KindNumber, KindBool and KindJSON. A NULL is then told from
// the text "NULL", and a number's text is its literal. The host sorts by them and writes them into
// JSON. A kinds string of the wrong length, or with another byte, makes its message malformed.
//
// host.call and plugin.result are a request and its answer made of two notifications, matched by
// call, a number the plugin chooses and does not reuse while it waits. The host runs only the
// command ids the manifest's calls = [...] declares, and answers every call it accepts exactly once:
// ok, or error "<code> <message>" with the code refused (an id not declared, a plugin's command, or
// more than MaxPendingCalls waiting: "refused busy"), unknown (no command has the id) or failed
// (the command's own error). A call reusing a number still waiting is a protocol violation: the
// host logs it, runs nothing, and sends no result for it, so the waiting call's is the only one
// with that number. Peer.Call does all of this for a plugin in Go.
//
// A card or dialog need not draw its cells by hand: plugin/card is a native kit of pages and
// controls (inputs, buttons, lists, trees), and plugin/decl runs a golib QML layout. Both draw in
// AutoDoc's look and theme (docs/plugins.md, "Drawing a card").
//
// A plugin in Go writes a [Handler] and calls [Serve]; one in another language speaks the table
// above. The frame's colours are the themes' vocabulary: "default", an ANSI name ("brightwhite"), or
// "#rrggbb" — golib's style.ParseColor reads them, as it reads the theme the plugin is sent.
package plugin

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// The plugin protocol's versions this SDK speaks. A host speaks to each plugin the one its manifest
// says, and Serve answers any from MinProtocol to Protocol with that same one. The protocol rises
// when a manifest can declare something an older host must not run without, for a changed meaning,
// or for something a plugin must handle (ADR 1791268009 §1).
const (
	MinProtocol = 1
	Protocol    = 8
)

// The notifications the host sends.
const (
	MethodOpen    = "plugin.open"
	MethodKey     = "plugin.key"
	MethodPointer = "plugin.pointer" // protocol 6: a plugin view's left press or vertical wheel
	MethodResize  = "plugin.resize"
	MethodTheme   = "plugin.theme"
	MethodClose   = "plugin.close"
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
	// MethodResult is the host's answer to a host.call, by its call number.
	MethodResult = "plugin.result"
	// MethodFetch asks for up to n more rows of the table id (protocol 4). The host sends another
	// only once host.rows (or host.table_end) has answered it.
	MethodFetch = "plugin.table.fetch"
	// MethodTableGone is the table id closed: its panel closed, or a newer table replaced it. The
	// plugin frees what serves its rows (a database cursor, say).
	MethodTableGone = "plugin.table.close"
)

// The notifications the plugin sends.
const (
	MethodReady     = "host.ready"
	MethodFrame     = "host.frame"
	MethodTitle     = "host.title"
	MethodHostClose = "host.close"
	// MethodCall asks the host to run a command of its catalog, one the manifest's calls declares.
	MethodCall = "host.call"
	// MethodSession replaces the plugin's session slot (protocol 7, session.go): kept by the host
	// in memory until AutoDoc quits, and handed back in each plugin.open.
	MethodSession = "host.session"
	// MethodTable shows a table (protocol 4): its id, title, fields and first rows, and whether
	// more remain. It replaces the table shown, any plugin's.
	MethodTable = "host.table"
	// MethodRows is the table's next rows, answering plugin.table.fetch, and whether more remain.
	MethodRows = "host.rows"
	// MethodTableEnd is the table's rows ended before the last was sent: no more will come, and
	// error, when set, says why.
	MethodTableEnd = "host.table_end"
)

// The bounds of host.call (protocol 3).
const (
	// MaxPendingCalls is how many of a plugin's calls the host keeps waiting; one more is answered
	// "refused busy".
	MaxPendingCalls = 64
	// CallTimeout is how long Peer.Call waits for the host's answer.
	CallTimeout = 10 * time.Second
)

// The codes of a plugin.result's error.
const (
	CallRefused = "refused" // not declared, a plugin's command, or too many waiting (busy)
	CallUnknown = "unknown" // no command has the id
	CallFailed  = "failed"  // the command's own error
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
	// Session is the slot as this plugin left it in this run of AutoDoc (protocol 7, session.go);
	// nil when it has none.
	Session map[string]string
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
	// CursorByte and SelectionBytes are Cursor and Selection as byte offsets into Text, each
	// selection range [start, end): the host counts them from the same grapheme clusters as the
	// columns, so a plugin slicing Text never counts clusters itself. Sent from protocol 4 on;
	// before it they are 0 and nil.
	CursorByte     int
	SelectionBytes [][2]int
}

// Key is a key typed into the dialog. Key is its text for a printable key (" " for Space), or one of
// Up, Down, Left, Right, Enter, Tab, Backspace, Delete, Insert, Home, End, PageUp, PageDown, F1 to
// F12. Esc is never sent: it closes the dialog.
type Key struct {
	Key, Text        string
	Ctrl, Alt, Shift bool
}

// Pointer is a view-local, zero-based left press or vertical wheel step. Count is capped by the
// host, and the plugin checks the coordinates against its most recently opened/resized frame.
type Pointer struct {
	Kind, Button string // "press"/"left", or "wheel"/"up"/"down"
	X, Y, Count  int
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
	if o.Session != nil {
		m["session"] = sessionWire(o.Session)
	}
	return one(m)
}

// FocusParams are plugin.focus's.
func FocusParams(focused bool) []any { return one(map[string]any{"focused": focused}) }

// CommandParams are plugin.command's.
func CommandParams(id string) []any { return one(map[string]any{"id": id}) }

func (p Position) wire() map[string]any { return map[string]any{"line": p.Line, "col": p.Col} }

// DocumentParams are plugin.document's as protocols 2 and 3 have it, without the byte offsets. A
// TooLarge one carries its path, workspace and version alone.
func DocumentParams(d Document) []any { return DocumentParamsAt(3, d) }

// DocumentParamsAt are plugin.document's as a plugin speaking protocol is sent it: from protocol 4
// on with cursor_byte and selection_bytes, so a plugin written for an earlier one sees what it
// always has.
func DocumentParamsAt(protocol int, d Document) []any { return one(documentWire(protocol, d)) }

func documentWire(protocol int, d Document) map[string]any {
	m := map[string]any{"path": d.Path, "workspace": d.Workspace, "version": d.Version}
	if d.TooLarge {
		m["too_large"] = true
		return m
	}
	sel := make([]any, len(d.Selection))
	for i, r := range d.Selection {
		sel[i] = map[string]any{"start": r.Start.wire(), "end": r.End.wire()}
	}
	m["text"], m["cursor"], m["selection"] = d.Text, d.Cursor.wire(), sel
	if protocol >= 4 {
		bytes := make([]any, len(d.SelectionBytes))
		for i, r := range d.SelectionBytes {
			bytes[i] = []any{r[0], r[1]}
		}
		m["cursor_byte"], m["selection_bytes"] = d.CursorByte, bytes
	}
	return m
}

// CommandDocParams are plugin.command's with the document it runs on (protocol 4): the note as it
// stood when the command was chosen, carried inside the command so no queue can drop it as it
// drops a waiting plugin.document (FitCommand sizes it).
func CommandDocParams(id string, d Document) []any {
	return one(map[string]any{"id": id, "document": documentWire(4, d)})
}

// KeyParams are plugin.key's.
func KeyParams(k Key) []any {
	return one(map[string]any{"key": k.Key, "text": k.Text, "ctrl": k.Ctrl, "alt": k.Alt, "shift": k.Shift})
}

// PointerParams are plugin.pointer's coordinates in the plugin view's frame.
func PointerParams(p Pointer) []any {
	return one(map[string]any{"kind": p.Kind, "button": p.Button, "x": p.X, "y": p.Y, "count": p.Count})
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

// Call is host.call: its number, the command's id and its arguments.
type Call struct {
	Num  int64
	ID   string
	Args []string
}

// Result is plugin.result: the call it answers, and whether the command ran; Error is
// "<code> <message>" when it did not.
type Result struct {
	Num   int64
	OK    bool
	Error string
}

// CallError is a call the host did not run, or whose command failed: its code (CallRefused,
// CallUnknown, CallFailed) and why.
type CallError struct{ Code, Message string }

func (e *CallError) Error() string { return strings.TrimSpace(e.Code + " " + e.Message) }

// callError reads a plugin.result's error.
func callError(s string) *CallError {
	code, msg, _ := strings.Cut(s, " ")
	return &CallError{Code: code, Message: msg}
}

// CallParams are host.call's.
func CallParams(c Call) []any {
	args := make([]any, len(c.Args))
	for i, a := range c.Args {
		args[i] = a
	}
	return one(map[string]any{"call": c.Num, "id": c.ID, "args": args})
}

// ResultParams are plugin.result's; error only when it did not run.
func ResultParams(r Result) []any {
	m := map[string]any{"call": r.Num, "ok": r.OK}
	if !r.OK {
		m["error"] = r.Error
	}
	return one(m)
}

// callNum is a call number: a positive whole number.
func callNum(m map[string]any) (int64, bool) {
	switch n := m["call"].(type) {
	case int64:
		return n, n > 0
	case uint64:
		return int64(n), n > 0 && n <= math.MaxInt64
	case int:
		return int64(n), n > 0
	}
	return 0, false
}

// ReadCall reads host.call. Its Num is set whenever the call number is readable, even when the rest
// is not, so a host can answer a malformed call by its number.
func ReadCall(params []any) (Call, error) {
	m, err := arg(params)
	if err != nil {
		return Call{}, err
	}
	n, ok := callNum(m)
	if !ok {
		return Call{}, fmt.Errorf("%w: host.call wants a call number, a positive whole number", ErrParams)
	}
	c := Call{Num: n, ID: str(m, "id")}
	if c.ID == "" {
		return c, fmt.Errorf("%w: host.call wants id", ErrParams)
	}
	raw, ok := m["args"].([]any)
	if !ok && m["args"] != nil {
		return c, fmt.Errorf("%w: host.call's args is a list of strings", ErrParams)
	}
	for _, a := range raw {
		s, ok := a.(string)
		if !ok {
			return c, fmt.Errorf("%w: host.call's args is a list of strings", ErrParams)
		}
		c.Args = append(c.Args, s)
	}
	return c, nil
}

// ReadResult reads plugin.result.
func ReadResult(params []any) (Result, error) {
	m, err := arg(params)
	if err != nil {
		return Result{}, err
	}
	n, ok := callNum(m)
	if !ok {
		return Result{}, fmt.Errorf("%w: plugin.result wants a call number", ErrParams)
	}
	return Result{Num: n, OK: flag(m, "ok"), Error: str(m, "error")}, nil
}

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

func num(m map[string]any, k string) (int, bool) { return number(m[k]) }

// number is a whole number as the decoder gives it: int64, or uint64 past it.
func number(v any) (int, bool) {
	switch n := v.(type) {
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
	session, err := readSessionMap(m["session"], false)
	if err != nil {
		return Open{}, err
	}
	_, hasW := m["width"]
	_, hasH := m["height"]
	if !hasW && !hasH {
		return Open{Protocol: p, Theme: readTheme(m["theme"]), Session: session}, nil
	}
	w, ok1 := num(m, "width")
	h, ok2 := num(m, "height")
	if !ok1 || !ok2 {
		return Open{}, fmt.Errorf("%w: plugin.open wants a width and a height, or neither", ErrParams)
	}
	return Open{Protocol: p, Width: w, Height: h, Theme: readTheme(m["theme"]), Session: session}, nil
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

// ReadDocument reads plugin.document. A selection entry that is not a map is dropped, and so is a
// byte range that is not two offsets.
func ReadDocument(params []any) (Document, error) {
	m, err := arg(params)
	if err != nil {
		return Document{}, err
	}
	return readDocument(m)
}

func readDocument(m map[string]any) (Document, error) {
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
	d.CursorByte, _ = num(m, "cursor_byte")
	spans, _ := m["selection_bytes"].([]any)
	for _, r := range spans {
		pair, _ := r.([]any)
		if len(pair) != 2 {
			continue
		}
		from, ok1 := number(pair[0])
		to, ok2 := number(pair[1])
		if ok1 && ok2 {
			d.SelectionBytes = append(d.SelectionBytes, [2]int{from, to})
		}
	}
	return d, nil
}

// ReadCommandDocument reads the document a protocol-4 plugin.command carries; false when it
// carries none (an earlier protocol's, or a plugin without the feed) or the one it carries is
// malformed.
func ReadCommandDocument(params []any) (Document, bool) {
	m, err := arg(params)
	if err != nil {
		return Document{}, false
	}
	dm, ok := m["document"].(map[string]any)
	if !ok {
		return Document{}, false
	}
	d, err := readDocument(dm)
	return d, err == nil
}

// ReadKey reads plugin.key.
func ReadKey(params []any) (Key, error) {
	m, err := arg(params)
	if err != nil {
		return Key{}, err
	}
	return Key{Key: str(m, "key"), Text: str(m, "text"), Ctrl: flag(m, "ctrl"), Alt: flag(m, "alt"), Shift: flag(m, "shift")}, nil
}

// ReadPointer refuses malformed or unsupported pointer actions before they reach a plugin.
func ReadPointer(params []any) (Pointer, error) {
	m, err := arg(params)
	if err != nil {
		return Pointer{}, err
	}
	x, xOK := num(m, "x")
	y, yOK := num(m, "y")
	count, countOK := num(m, "count")
	p := Pointer{Kind: str(m, "kind"), Button: str(m, "button"), X: x, Y: y, Count: count}
	if !xOK || !yOK || !countOK || x < 0 || y < 0 || count < 1 || count > 16 ||
		!(p.Kind == "press" && p.Button == "left" || p.Kind == "wheel" && (p.Button == "up" || p.Button == "down")) {
		return Pointer{}, fmt.Errorf("%w: plugin.pointer wants a left press or vertical wheel, nonnegative coordinates and 1–16 steps", ErrParams)
	}
	return p, nil
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

// ---------------------------------------------------------------- tables (protocol 4)

// TableOpen is host.table: a table the host shows in its results panel, by the plugin's id for it.
// Fields are the columns' titles; each row has a cell per field (a shorter row's last cells are
// empty, a longer one's extra cells dropped). More says rows remain, for the host to fetch.
//
// Kinds (protocol 5) are each row's cells' kinds: Kinds[i] is row i's, one byte per field (see
// KindText). nil sends none, and every cell is text. A table's kinds are decided by its host.table:
// one sent with kinds (an empty list when it has no rows yet) reads them on every later page, and
// one sent without them reads every cell as text.
type TableOpen struct {
	ID, Title string
	Fields    []string
	Rows      [][]string
	Kinds     []string
	More      bool
}

// The kinds of a table's cells (protocol 5), one byte each: golib's widget.TableCellKind bytes.
const (
	KindText   byte = 't'
	KindNull   byte = 'z' // the cell is NULL; its text is what shows (the plugin's "NULL", say)
	KindNumber byte = 'n' // the cell's text is the number's literal
	KindBool   byte = 'b' // the cell's text is true or false
	KindJSON   byte = 'j' // the cell's text is a JSON value
)

// CheckKinds is why kinds cannot be the kinds of rows of a table of fields fields ("" when they
// can): one string per row, each exactly fields bytes, every byte a kind.
func CheckKinds(kinds []string, rows, fields int) string {
	if len(kinds) != rows {
		return fmt.Sprintf("%d kinds strings for %d rows", len(kinds), rows)
	}
	for i, k := range kinds {
		if len(k) != fields {
			return fmt.Sprintf("row %d's kinds are %d bytes for %d fields", i, len(k), fields)
		}
		for j := range len(k) {
			switch k[j] {
			case KindText, KindNull, KindNumber, KindBool, KindJSON:
			default:
				return fmt.Sprintf("row %d's kinds hold %q, not a kind", i, k[j])
			}
		}
	}
	return ""
}

// TableFetch is plugin.table.fetch: up to N more rows of the table ID.
type TableFetch struct {
	ID string
	N  int
}

func rowsWire(rows [][]string) []any {
	out := make([]any, len(rows))
	for i, row := range rows {
		cells := make([]any, len(row))
		for j, c := range row {
			cells[j] = c
		}
		out[i] = cells
	}
	return out
}

func stringsWire(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// TableParams are host.table's; kinds only when t has them.
func TableParams(t TableOpen) []any {
	m := map[string]any{"id": t.ID, "title": t.Title, "fields": stringsWire(t.Fields), "rows": rowsWire(t.Rows), "more": t.More}
	if t.Kinds != nil {
		m["kinds"] = stringsWire(t.Kinds)
	}
	return one(m)
}

// RowsParams are host.rows'.
func RowsParams(id string, rows [][]string, more bool) []any {
	return RowsKindsParams(id, rows, nil, more)
}

// RowsKindsParams are host.rows' with each row's kinds (protocol 5); nil kinds sends none.
func RowsKindsParams(id string, rows [][]string, kinds []string, more bool) []any {
	m := map[string]any{"id": id, "rows": rowsWire(rows), "more": more}
	if kinds != nil {
		m["kinds"] = stringsWire(kinds)
	}
	return one(m)
}

// TableEndParams are host.table_end's; error only when there is one.
func TableEndParams(id, why string) []any {
	m := map[string]any{"id": id}
	if why != "" {
		m["error"] = why
	}
	return one(m)
}

// FetchParams are plugin.table.fetch's.
func FetchParams(f TableFetch) []any { return one(map[string]any{"id": f.ID, "n": f.N}) }

// TableGoneParams are plugin.table.close's.
func TableGoneParams(id string) []any { return one(map[string]any{"id": id}) }

// tableID is a table notification's id: a string that is not empty.
func tableID(m map[string]any, method string) (string, error) {
	id := str(m, "id")
	if id == "" {
		return "", fmt.Errorf("%w: %s wants id", ErrParams, method)
	}
	return id, nil
}

// readRows reads a table's rows. A row that is not a list is dropped, and a cell that is not a
// string reads as empty; dropped counts both, so the host can say so. A row dropped is kept out of
// kept, the indices of the rows read, so their kinds can follow them.
func readRows(v any, method string) (rows [][]string, kept []int, dropped int, err error) {
	raw, ok := v.([]any)
	if !ok && v != nil {
		return nil, nil, 0, fmt.Errorf("%w: %s's rows is a list of lists of strings", ErrParams, method)
	}
	rows = make([][]string, 0, len(raw))
	for i, r := range raw {
		cells, ok := r.([]any)
		if !ok {
			dropped++
			continue
		}
		kept = append(kept, i)
		row := make([]string, len(cells))
		for j, c := range cells {
			s, ok := c.(string)
			if !ok {
				dropped++
			}
			row[j] = s
		}
		rows = append(rows, row)
	}
	return rows, kept, dropped, nil
}

// readKinds reads a message's kinds (protocol 5): nil when it has none, else one string per row
// sent, of which those of the rows read (kept) are returned, in their order. A message whose kinds
// are not a list of strings, one per row sent, is malformed; their length and bytes are CheckKinds'.
func readKinds(m map[string]any, sent int, kept []int, method string) ([]string, error) {
	v, ok := m["kinds"]
	if !ok || v == nil {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok || len(raw) != sent {
		return nil, fmt.Errorf("%w: %s's kinds is a list of strings, one per row", ErrParams, method)
	}
	all := make([]string, len(raw))
	for i, k := range raw {
		if all[i], ok = k.(string); !ok {
			return nil, fmt.Errorf("%w: %s's kinds are strings", ErrParams, method)
		}
	}
	out := make([]string, 0, len(kept))
	for _, i := range kept {
		out = append(out, all[i])
	}
	return out, nil
}

// sentRows is how many rows a message's rows list holds, the malformed ones included.
func sentRows(m map[string]any) int {
	raw, _ := m["rows"].([]any)
	return len(raw)
}

// ReadTable reads host.table as protocol 4 has it, with no kinds; dropped counts its malformed rows
// and cells (readRows).
func ReadTable(params []any) (t TableOpen, dropped int, err error) { return ReadTableAt(params, 4) }

// ReadTableAt reads host.table from a plugin speaking protocol: from 5, with its kinds, which must
// fit its rows and fields (CheckKinds) or the table is malformed. Below 5 a kinds field is not read.
func ReadTableAt(params []any, protocol int) (t TableOpen, dropped int, err error) {
	m, err := arg(params)
	if err != nil {
		return TableOpen{}, 0, err
	}
	if t.ID, err = tableID(m, MethodTable); err != nil {
		return TableOpen{}, 0, err
	}
	raw, ok := m["fields"].([]any)
	if !ok || len(raw) == 0 {
		return TableOpen{}, 0, fmt.Errorf("%w: host.table wants fields, a list of one or more titles", ErrParams)
	}
	for _, f := range raw {
		s, ok := f.(string)
		if !ok {
			return TableOpen{}, 0, fmt.Errorf("%w: host.table's fields are strings", ErrParams)
		}
		t.Fields = append(t.Fields, s)
	}
	var kept []int
	if t.Rows, kept, dropped, err = readRows(m["rows"], MethodTable); err != nil {
		return TableOpen{}, 0, err
	}
	if protocol >= 5 {
		if t.Kinds, err = readKinds(m, sentRows(m), kept, MethodTable); err != nil {
			return TableOpen{}, 0, err
		}
		if why := CheckKinds(t.Kinds, len(t.Rows), len(t.Fields)); t.Kinds != nil && why != "" {
			return TableOpen{}, 0, fmt.Errorf("%w: host.table's kinds: %s", ErrParams, why)
		}
	}
	t.Title, t.More = str(m, "title"), flag(m, "more")
	return t, dropped, nil
}

// TableRows is host.rows as protocol 5 reads it: table ID's next rows, their kinds (nil: none
// sent), and whether more remain.
type TableRows struct {
	ID    string
	Rows  [][]string
	Kinds []string
	More  bool
}

// ReadRows reads host.rows as protocol 4 has it: the table's id, its next rows and whether more
// remain; dropped counts the malformed rows and cells (readRows).
func ReadRows(params []any) (id string, rows [][]string, more bool, dropped int, err error) {
	r, dropped, err := ReadTableRows(params, 4)
	return r.ID, r.Rows, r.More, dropped, err
}

// ReadTableRows reads host.rows from a plugin speaking protocol: from 5, with its kinds, one string
// per row. Their length is checked against the table's fields by whoever knows them, the host
// (CheckKinds), since host.rows does not carry the fields. Below 5 a kinds field is not read.
func ReadTableRows(params []any, protocol int) (r TableRows, dropped int, err error) {
	m, err := arg(params)
	if err != nil {
		return TableRows{}, 0, err
	}
	if r.ID, err = tableID(m, MethodRows); err != nil {
		return TableRows{}, 0, err
	}
	var kept []int
	if r.Rows, kept, dropped, err = readRows(m["rows"], MethodRows); err != nil {
		return TableRows{}, 0, err
	}
	if protocol >= 5 {
		if r.Kinds, err = readKinds(m, sentRows(m), kept, MethodRows); err != nil {
			return TableRows{}, 0, err
		}
	}
	r.More = flag(m, "more")
	return r, dropped, nil
}

// ReadTableEnd reads host.table_end: the table's id, and why its rows ended ("" when no error).
func ReadTableEnd(params []any) (id, why string, err error) {
	m, err := arg(params)
	if err != nil {
		return "", "", err
	}
	if id, err = tableID(m, MethodTableEnd); err != nil {
		return "", "", err
	}
	return id, str(m, "error"), nil
}

// ReadFetch reads plugin.table.fetch.
func ReadFetch(params []any) (TableFetch, error) {
	m, err := arg(params)
	if err != nil {
		return TableFetch{}, err
	}
	id, err := tableID(m, MethodFetch)
	if err != nil {
		return TableFetch{}, err
	}
	n, ok := num(m, "n")
	if !ok || n < 1 {
		return TableFetch{}, fmt.Errorf("%w: plugin.table.fetch wants n, a count of rows", ErrParams)
	}
	return TableFetch{ID: id, N: n}, nil
}

// ReadTableGone reads plugin.table.close: the table's id.
func ReadTableGone(params []any) (string, error) {
	m, err := arg(params)
	if err != nil {
		return "", err
	}
	return tableID(m, MethodTableGone)
}
