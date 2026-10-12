package tui

import (
	"maps"
	"os"
	"strconv"
)

// terminals owns shell startup and keyboard restoration. The host contributes
// only its PanelHost services; none of this state is shared between programs.
type terminals struct {
	c       PanelHost
	started bool
	restore bool
	before  string
}

func newTerminals(c PanelHost) *terminals { return &terminals{c: c, restore: true} }
func (*terminals) Name() string           { return "terminals" }
func (t *terminals) State() map[string]any {
	st := map[string]any{
		"App.terminalDir":         "",
		"App.terminalShown":       false,
		"App.terminalEdges":       choices(termEdgeLabels...),
		"App.terminalEdgeIndex":   0,
		"App.terminalSizes":       choices(percentLabels(termSizes)...),
		"App.terminalSizeIndex":   nearest(termSizes, 30),
		"App.terminalLengths":     choices(percentLabels(termLengths)...),
		"App.terminalLengthIndex": nearest(termLengths, 100),
	}
	maps.Copy(st, termState(t.c.Prefs()))
	return st
}
func (t *terminals) Commands() map[string]Command {
	return map[string]Command{
		"App.toggleTerminal":    none(t.toggleTerminal),
		"App.openTerminal":      none(t.openTerminal),
		"App.terminalExited":    oneNumber("an exit code", t.terminalExited),
		"App.setTerminalEdge":   oneNumber("a row", t.setTerminalEdge),
		"App.setTerminalSize":   oneNumber("a row", t.setTerminalSize),
		"App.setTerminalLength": oneNumber("a row", t.setTerminalLength),
	}
}
func (t *terminals) Panels() []PanelSpec {
	s := terminalPanelSpec()
	s.Open = func(PanelHost) { t.openTerminalPanel() }
	s.Close = func(PanelHost) { t.closeTerminalPanel() }
	s.Opened = func(c PanelHost) { c.Set("App.terminalShown", true) }
	s.Closed = func(PanelHost) { t.terminalPanelClosed() }
	return []PanelSpec{s}
}
func (t *terminals) PreferencesChanged() {
	for name, value := range termState(t.c.Prefs()) {
		t.c.Set(name, value)
	}
	t.syncTermDialog()
}

// THE TERMINAL — a shell in a drawer over the page (SPC `, Go › Terminal): at the bottom, or the
// top, a side, or centred, as its preference says, each place with its own size. It starts in the
// workspace's folder the first time it opens and keeps running while hidden. Opening it gives it
// the keyboard; hiding it gives the keyboard back to the pane that had it, and hiding it while
// another pane has the keyboard moves nothing. Its keys are golib's Terminal's: everything goes to
// the shell, and Ctrl+\ Ctrl+n (or Esc in the Vim editor mode, outside full-screen programs) leaves
// for Normal mode over its history.

// The terminal's preference: where it opens. Its sizes are the panels' one way (panelsize.go), and
// its defaults and bounds are its spec's (panels.go).
const prefTermEdge = "tui.terminal.edge"

// termEdges are where the terminal opens, in the order the Preferences dialog offers them: the
// Drawer's edges and its centre.
var termEdges = []string{"bottom", "top", "left", "right", "center"}

var termEdgeLabels = []string{"bottom", "top", "left", "right", "centre"}

// The choices the dialog offers for a size and a length, in percent, every tenth.
var (
	termSizes   = percents(10, 90)
	termLengths = percents(10, 100)
)

// percentLabels are the choices as the dialog shows them.
func percentLabels(ps []int) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = strconv.Itoa(p) + "%"
	}
	return out
}

func percents(lo, hi int) []int {
	var out []int
	for p := lo; p <= hi; p += 10 {
		out = append(out, p)
	}
	return out
}

// termDefault is the terminal's size and length at a place before the user sets them: a third of
// the Window's height at the top or bottom, two fifths of its width at a side, both the full
// length; 70 by 70 centred.
func termDefault(edge string) (size, length int) {
	switch edge {
	case "left", "right":
		return 40, 100
	case "center":
		return 70, 70
	}
	return 30, 100
}

// readTermPrefs reads where the terminal opens, keeping the default for a place it cannot use.
func readTermPrefs(p *prefs, m map[string]any) {
	if s, _ := m[prefTermEdge].(string); indexOfOK(termEdges, s) {
		p.termEdge = s
	}
}

// pct is a stored percentage in [lo, hi].
func pct(v any, lo, hi int) (int, bool) {
	s, _ := v.(string)
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= lo && n <= hi
}

func indexOfOK(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// termState is what the document reads of the terminal's preferences.
func termState(p prefs) map[string]any {
	size, length := p.panelGeometry("terminal", p.termEdge)
	return map[string]any{
		"App.terminalEdge":   p.termEdge,
		"App.terminalSize":   size,
		"App.terminalLength": length,
	}
}

// syncTermDialog sets the Preferences dialog's terminal choosers.
func (t *terminals) syncTermDialog() {
	p := t.c.Prefs()
	size, length := p.panelGeometry("terminal", p.termEdge)
	t.c.Set("App.terminalEdgeIndex", indexOf(termEdges, p.termEdge))
	t.c.Set("App.terminalSizeIndex", nearest(termSizes, size))
	t.c.Set("App.terminalLengthIndex", nearest(termLengths, length))
}

// nearest is the row of the choice closest to v.
func nearest(choices []int, v int) int {
	best := 0
	for i, c := range choices {
		if abs(c-v) < abs(choices[best]-v) {
			best = i
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (t *terminals) setTerminalEdge(i int) {
	if i >= 0 && i < len(termEdges) {
		e := termEdges[i]
		t.c.KeepPreference(prefTermEdge, e, func(p *prefs) { p.termEdge = e })
		t.c.DockPanel("terminal")
	}
}

// setTerminalSize and setTerminalLength set the size at the place the terminal opens from now, the
// panels' one way.
func (t *terminals) setTerminalSize(i int) {
	if i >= 0 && i < len(termSizes) {
		t.c.KeepPanelGeometry("terminal", "size", termSizes[i])
	}
}

func (t *terminals) setTerminalLength(i int) {
	if i >= 0 && i < len(termLengths) {
		t.c.KeepPanelGeometry("terminal", "length", termLengths[i])
	}
}

// withEntry is m with k set to v, as a new map: prefs are values, and setPref changes a copy.
func withEntry(m map[string]int, k string, v int) map[string]int {
	out := make(map[string]int, len(m)+1)
	for key, val := range m {
		out[key] = val
	}
	out[k] = v
	return out
}

// openTerminalPanel is the terminal's spec Open (panels.go): it remembers which pane had the
// keyboard, for hiding it to give it back, opens the drawer, starts the shell the first time, and
// gives the terminal the keyboard. SPC ` and Go › Terminal are togglePanel, which comes here.
func (t *terminals) openTerminalPanel() {
	t.before = t.c.KeyboardPane()
	t.c.Keep(t.c.CallView("terminal", "open"))
	if !t.started {
		if ws, ok := t.c.ActiveWorkspace(); ok {
			t.c.Set("App.terminalDir", termDir(ws.root))
		}
		// Started only once it starts: a shell that fails to launch is tried again on the next
		// open (and Enter in the pane tries it at once).
		if err := t.c.CallView("terminalView", "start"); err != nil {
			t.c.Notify("the terminal could not start (" + err.Error() + "): SPC ` or Enter in it tries again")
		} else {
			t.started = true
		}
	}
	t.c.Focus("terminalView")
}

// closeTerminalPanel is the terminal's spec Close: it remembers whether the terminal had the
// keyboard, for the close to give it back, and closes the drawer.
func (t *terminals) closeTerminalPanel() {
	t.restore = t.c.Focused("terminalView")
	t.c.CloseDrawer("terminal")
}

// terminalPanelClosed is the terminal's spec Closed: the drawer is closed, however it closed
// (Escape inside it, SPC ` from it), and the keyboard goes back where it was.
func (t *terminals) terminalPanelClosed() {
	t.c.Set("App.terminalShown", false)
	t.terminalClosed()
}

// toggleTerminal is SPC ` and Go › Terminal: togglePanel, the one way (panels.go).
func (t *terminals) toggleTerminal() { t.c.TogglePanel("terminal") }

// openTerminal is the command line's terminal: the terminal panel open with the keyboard in it,
// opening it if it is closed (SPC ` toggles it).
func (t *terminals) openTerminal() {
	if t.c.PanelOpen("terminal") {
		t.c.Focus("terminalView")
		return
	}
	t.toggleTerminal()
}

// openTerminal keeps the editor command entry point while its catalog moves.
// Startup and focus behavior belong to the terminals feature.
func (h *Host) openTerminal() { h.terminals.openTerminal() }

// termDir is where the shell starts: the workspace's folder when this machine has it as a folder
// (a daemon may serve a root only it can see), else the user's home.
func termDir(root string) string {
	if fi, err := os.Stat(root); err == nil && fi.IsDir() {
		return root
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// terminalClosed gives the keyboard back to the pane that had it when the terminal opened, when
// the terminal had it as it closed (Escape inside it, or SPC ` from it).
func (t *terminals) terminalClosed() {
	restore := t.restore
	t.restore = true // Escape inside the terminal closes it without passing toggleTerminal
	if !restore {
		return
	}
	target := t.before
	if target == "" {
		target = "editor"
	}
	t.c.Focus(target)
}

// keyboardPanes are the panes the keyboard can come back to from the terminal: the page, then the
// panels that take it back.
func keyboardPanes(specs []PanelSpec) []string {
	out := []string{"editor"}
	for _, s := range specs {
		if s.Keyboard {
			out = append(out, s.View)
		}
	}
	return out
}

// keyboardPane is the pane with the keyboard now, of those; "" for another (a dialog's field).
func (h *Host) keyboardPane() string {
	for _, id := range keyboardPanes(h.panelSpecs()) {
		if h.paneHasKeyboard(id) {
			return id
		}
	}
	return ""
}

func (h *Host) paneHasKeyboard(id string) bool {
	c, ok := h.p.Find(id)
	return ok && h.p.App().FocusWithin(c)
}

// terminalExited says the shell is gone, and how to start another.
func (t *terminals) terminalExited(code int) {
	t.c.Notify("the terminal's shell exited (" + strconv.Itoa(code) + "): Enter in it starts another")
}
