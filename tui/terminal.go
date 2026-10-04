package tui

import (
	"os"
	"strconv"
)

// THE TERMINAL — a shell in a drawer over the page (SPC `, View › Terminal): at the bottom, or the
// top, a side, or centred, as its preference says, each place with its own size. It starts in the
// workspace's folder the first time it opens and keeps running while hidden. Opening it gives it
// the keyboard; hiding it gives the keyboard back to the pane that had it, and hiding it while
// another pane has the keyboard moves nothing. Its keys are golib's Terminal's: everything goes to
// the shell, and Ctrl+\ Ctrl+n (or Esc in the Vim editor mode, outside full-screen programs) leaves
// for Normal mode over its history.

// The terminal's preferences: where it opens, and at each place its size across the edge and its
// length along it, in percent of the Window.
const (
	prefTermEdge   = "tui.terminal.edge"
	prefTermPrefix = "tui.terminal."
	prefTermSize   = ".size"
	prefTermLength = ".length"
)

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

// termDefault is a place's size and length before the user sets them: a third of the Window's
// height at the top or bottom, two fifths of its width at a side, both the full length; 70 by 70
// centred.
func termDefault(edge string) (size, length int) {
	switch edge {
	case "left", "right":
		return 40, 100
	case "center":
		return 70, 70
	}
	return 30, 100
}

// termGeometry is the terminal's size and length at an edge: the stored ones, or that edge's
// defaults, so a size set at the bottom never stretches a centred pane.
func (p prefs) termGeometry(edge string) (size, length int) {
	size, length = termDefault(edge)
	if v, ok := p.termSize[edge]; ok {
		size = v
	}
	if v, ok := p.termLength[edge]; ok {
		length = v
	}
	return size, length
}

// readTermPrefs reads the terminal's preferences from the store's, keeping the defaults for
// anything it cannot use.
func readTermPrefs(p *prefs, m map[string]any) {
	if s, _ := m[prefTermEdge].(string); indexOfOK(termEdges, s) {
		p.termEdge = s
	}
	for _, edge := range termEdges {
		if n, ok := pct(m[prefTermPrefix+edge+prefTermSize], 10, 90); ok {
			p.termSize[edge] = n
		}
		if n, ok := pct(m[prefTermPrefix+edge+prefTermLength], 10, 100); ok {
			p.termLength[edge] = n
		}
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
	size, length := p.termGeometry(p.termEdge)
	return map[string]any{
		"App.terminalEdge":   p.termEdge,
		"App.terminalSize":   size,
		"App.terminalLength": length,
	}
}

// syncTermDialog sets the Preferences dialog's terminal choosers.
func (h *Host) syncTermDialog() {
	size, length := h.prefs.termGeometry(h.prefs.termEdge)
	h.set("App.terminalEdgeIndex", indexOf(termEdges, h.prefs.termEdge))
	h.set("App.terminalSizeIndex", nearest(termSizes, size))
	h.set("App.terminalLengthIndex", nearest(termLengths, length))
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

func (h *Host) setTerminalEdge(i int) {
	if i >= 0 && i < len(termEdges) {
		e := termEdges[i]
		h.setPref(prefTermEdge, e, func(p *prefs) { p.termEdge = e })
	}
}

// setTerminalSize and setTerminalLength set the size at the edge the terminal opens from now.
func (h *Host) setTerminalSize(i int) {
	if i >= 0 && i < len(termSizes) {
		edge, n := h.prefs.termEdge, termSizes[i]
		h.setPref(prefTermPrefix+edge+prefTermSize, strconv.Itoa(n), func(p *prefs) {
			p.termSize = withEntry(p.termSize, edge, n)
		})
	}
}

func (h *Host) setTerminalLength(i int) {
	if i >= 0 && i < len(termLengths) {
		edge, n := h.prefs.termEdge, termLengths[i]
		h.setPref(prefTermPrefix+edge+prefTermLength, strconv.Itoa(n), func(p *prefs) {
			p.termLength = withEntry(p.termLength, edge, n)
		})
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

// toggleTerminal is SPC ` and View › Terminal. Opening it the first time starts the shell, in
// the workspace's folder; opening it remembers which pane had the keyboard, for hiding it to give
// it back.
func (h *Host) toggleTerminal() {
	if h.panelOpen["terminal"] {
		h.termRestore = h.paneHasKeyboard("terminalView")
		h.keep(h.p.Call("terminal", "close"))
		return
	}
	h.termBefore = h.keyboardPane()
	h.keep(h.p.Call("terminal", "open"))
	if !h.termStarted {
		if ws, ok := h.activeWorkspaceInfo(); ok {
			h.set("App.terminalDir", termDir(ws.root))
		}
		// Started only once it starts: a shell that fails to launch is tried again on the next
		// open (and Enter in the pane tries it at once).
		if err := h.p.Call("terminalView", "start"); err != nil {
			h.notify("the terminal could not start (" + err.Error() + "): SPC ` or Enter in it tries again")
		} else {
			h.termStarted = true
		}
	}
	h.keep(h.p.Call("terminalView", "forceActiveFocus"))
}

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
func (h *Host) terminalClosed() {
	restore := h.termRestore
	h.termRestore = true // Escape inside the terminal closes it without passing toggleTerminal
	if !restore {
		return
	}
	target := h.termBefore
	if target == "" {
		target = "editor"
	}
	h.keep(h.p.Call(target, "forceActiveFocus"))
}

// keyboardPanes are the panes the keyboard can come back to from the terminal.
var keyboardPanes = []string{"editor", "explorerTree", "linksList"}

// keyboardPane is the pane with the keyboard now, of those; "" for another (a dialog's field).
func (h *Host) keyboardPane() string {
	for _, id := range keyboardPanes {
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
func (h *Host) terminalExited(code int) {
	h.notify("the terminal's shell exited (" + strconv.Itoa(code) + "): Enter in it starts another")
}
