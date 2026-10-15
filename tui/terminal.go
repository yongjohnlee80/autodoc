package tui

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// terminals owns shell startup and keyboard restoration. The host contributes
// only its PanelHost services; none of this state is shared between programs.
type terminals struct {
	c       PanelHost
	restore bool
	before  string
}

// terminalRuntimeHost is the PanelHost with the terminal panel's tabs (terminalpanel.go).
type terminalRuntimeHost interface {
	PanelHost
	Terminals(string) (TerminalPanel, error)
}

func newTerminals(c PanelHost) *terminals { return &terminals{c: c, restore: true} }
func (t *terminals) BindPanelHost(c PanelHost) error {
	t.c = c
	return nil
}
func (*terminals) Name() string { return "terminals" }
func (t *terminals) State() map[string]any {
	st := map[string]any{
		"App.terminalShown":   false,
		"App.terminalEdges":   choices(termEdgeLabels...),
		"App.terminalSizes":   choices(percentLabels(termSizes)...),
		"App.terminalLengths": choices(percentLabels(termLengths)...),
		"App.terminalThemes":  choices(t.themeLabels()...),
	}
	maps.Copy(st, t.lookState())
	return st
}
func (t *terminals) Prefs() []PrefSpec { return terminalPrefRows(t) }

func (t *terminals) Catalog() []Entry {
	open := builtin("terminal.open", "open the terminal", "App.openTerminal()", t.openTerminal)
	open.Verb, open.Aliases = "terminal", []string{"term"}
	open.Help = "open the terminal (SPC `)"
	return []Entry{
		builtin("terminal.toggle", "the terminal", "App.toggleTerminal()", t.toggleTerminal),
		open,
	}
}

func terminalPrefRows(t *terminals) []PrefSpec {
	row := choicePref(prefTermEdge, "bottom", termEdges, func(p *prefs) *string { return &p.termEdge }).
		shows(terminalPreferenceState)
	names := termThemes()
	if t != nil {
		names = t.themeNames()
	}
	theme := choicePref(prefTermTheme, "page", names, func(p *prefs) *string { return &p.termTheme }).
		shows(func(p prefs) map[string]any {
			return map[string]any{"App.terminalThemeIndex": max(indexOf(names, cmp.Or(p.termTheme, "page")), 0)}
		})
	if t != nil {
		row = row.then(func(prefs) { t.c.DockPanel("terminal") })
		theme = theme.then(func(prefs) { t.showLook() })
	}
	return []PrefSpec{row, theme}
}

// THE TERMINALS' THEME — the colours the terminal's and the agents' shells are drawn in, on their
// own: the page's (the default), or one of the themes golib ships, chosen in Preferences. It is
// their default text, background and cursor, and only theirs: the panel's frame and tab strip stay
// the page's, and the minimum contrast raises a program's colours against the theme's background.
// A program's own 16 colours (ls's, a prompt's) stay the program's: no theme recolours them.

const prefTermTheme = "tui.terminal.theme"

// termThemes are the choices, "page" first.
func termThemes() []string { return append([]string{"page"}, themeNames...) }

func termThemeLabels() []string { return append([]string{"the page's"}, themeNames...) }

func (t *terminals) themeNames() []string {
	if c, ok := t.c.(interface{ ThemeNames() []string }); ok {
		return append([]string{"page"}, c.ThemeNames()...)
	}
	return termThemes()
}
func (t *terminals) themeLabels() []string {
	if c, ok := t.c.(interface{ ThemeLabels() []string }); ok {
		return append([]string{"the page's"}, c.ThemeLabels()...)
	}
	return termThemeLabels()
}

// termLook is the theme the terminals are drawn in for p on a page drawn in page: its document's
// text, background and cursor colours. One golib does not ship falls back to the page's, and a page
// theme it does not ship (a layout of one's own) to its first.
func termLook(p prefs, page string) map[string]any {
	name := p.termTheme
	if name == "" || name == "page" || !isTheme(name) {
		name = page
	}
	if !isTheme(name) {
		name = themeNames[0]
	}
	v, _ := themes.Values(name)
	return map[string]any{
		"App.terminalBase":   v["document.base"],
		"App.terminalText":   v["document.text"],
		"App.terminalCursor": v["document.cursor"],
	}
}

// lookState is the terminals' colours now.
func (t *terminals) lookState() map[string]any {
	page := ""
	if c, ok := t.c.(interface{ PageTheme() string }); ok {
		page = c.PageTheme()
	}
	var p prefs
	if t.c != nil {
		p = t.c.Prefs()
	}
	if c, ok := t.c.(interface {
		ThemeValues(string) (map[string]string, error)
	}); ok {
		name := p.termTheme
		if name == "" || name == "page" {
			name = page
		}
		v, err := c.ThemeValues(name)
		if err != nil {
			v, err = c.ThemeValues(page)
		}
		if err == nil {
			return map[string]any{"App.terminalBase": v["document.base"], "App.terminalText": v["document.text"], "App.terminalCursor": v["document.cursor"]}
		}
	}
	return termLook(p, page)
}

// showLook puts the terminals' colours on screen: a theme chosen, the page's changed.
func (t *terminals) showLook() {
	for k, v := range t.lookState() {
		t.c.Set(k, v)
	}
}

// ThemeChanged is the page's theme switched (View › Theme): a terminal theme of the page's follows.
func (t *terminals) ThemeChanged() { t.showLook() }

func terminalPreferenceState(p prefs) map[string]any {
	st := termState(p)
	size, length := p.panelGeometry("terminal", p.termEdge)
	maps.Copy(st, map[string]any{
		"App.terminalEdgeIndex":   indexOf(termEdges, p.termEdge),
		"App.terminalSizeIndex":   nearest(termSizes, size),
		"App.terminalLengthIndex": nearest(termLengths, length),
	})
	return st
}
func (t *terminals) Commands() map[string]Command {
	return map[string]Command{
		"App.toggleTerminal":    none(t.toggleTerminal),
		"App.openTerminal":      none(t.openTerminal),
		"App.setTerminalEdge":   oneNumber("a row", t.setTerminalEdge),
		"App.setTerminalSize":   oneNumber("a row", t.setTerminalSize),
		"App.setTerminalLength": oneNumber("a row", t.setTerminalLength),
		"App.setTerminalTheme":  oneNumber("a row", t.setTerminalTheme),
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
	t.showLook()
}

// THE TERMINAL — shells in a drawer over the page (SPC `, Go › Terminal), each in a tab of its own:
// at the bottom, or the top, a side, or centred, as its preference says, each place with its own
// size. Opening it with no shell starts one in the workspace's folder; the shells keep running
// while hidden. + adds another; a tab's menu goes to another tab, adds one, or closes it. Opening
// it gives the keyboard to its shell; hiding it gives the keyboard back to the pane that had it,
// and hiding it while another pane has the keyboard moves nothing. Its keys are golib's
// Terminal's: everything goes to the shell, Esc included, and Ctrl+T (main.qml's modeKey) or
// Ctrl+\ Ctrl+n leaves for Normal mode over its history; Ctrl+T goes back.

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

// pct is a stored percentage in [lo, hi].
func pct(v any, lo, hi int) (int, bool) {
	s, _ := v.(string)
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= lo && n <= hi
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
		t.c.SetPref(prefTermEdge, e)
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

// setTerminalTheme is the Preferences row: the terminals' theme, kept.
func (t *terminals) setTerminalTheme(i int) {
	if all := t.themeNames(); i >= 0 && i < len(all) {
		t.c.SetPref(prefTermTheme, all[i])
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
// keyboard, for hiding it to give it back, opens the drawer, starts a shell when none runs, and
// gives the shell the keyboard. SPC ` and Go › Terminal are togglePanel, which comes here.
func (t *terminals) openTerminalPanel() {
	t.before = t.c.KeyboardPane()
	t.c.Keep(t.c.CallView("terminal", "open"))
	panel, err := t.panel()
	if err != nil {
		t.c.Keep(err)
		return
	}
	// a shell that failed to launch is dropped, and a fresh one tried: its command is read again
	// ($SHELL fixed since, say); Enter in its tab tries it at once
	for _, tab := range panel.Tabs() {
		if !tab.Running && !tab.Closing && tab.StartError != nil {
			_, err := panel.Remove(tab.ID)
			t.c.Keep(err)
		}
	}
	if id := liveShell(panel, panel.Active()); id != "" {
		t.c.Keep(panel.Select(id))
		return
	}
	t.newShell()
}

// panel is the terminal's tabs, its strip's + and menu given (tabmenus.go): a rebuilt parent has
// none of the old one's, so every lookup gives them again.
func (t *terminals) panel() (TerminalPanel, error) {
	c, ok := t.c.(terminalRuntimeHost)
	if !ok {
		return nil, fmt.Errorf("the terminal has no tabs to run in: %w", errs.ErrPrecondition)
	}
	panel, err := c.Terminals("terminalView")
	if err != nil {
		return nil, err
	}
	panel.Actions(t.newShell, nil)
	panel.Menus(nil, t.tabRows)
	return panel, nil
}

// liveShell is a tab of panel neither closing nor failed to start: prefer if it is one, else the
// first; "" for none.
func liveShell(panel TerminalPanel, prefer widget.TerminalTabID) widget.TerminalTabID {
	first := widget.TerminalTabID("")
	for _, tab := range panel.Tabs() {
		if tab.Closing || (!tab.Running && tab.StartError != nil) {
			continue
		}
		if tab.ID == prefer {
			return tab.ID
		}
		if first == "" {
			first = tab.ID
		}
	}
	return first
}

// newShell adds a shell, in the workspace's folder when this machine has it, starts it and gives
// it the keyboard: opening the panel with none, its +, and New on a tab's menu. A shell that fails
// to start says so, in its tab and in a notice.
func (t *terminals) newShell() {
	panel, err := t.panel()
	if err != nil {
		t.c.Keep(err)
		return
	}
	root := ""
	if ws, ok := t.c.ActiveWorkspace(); ok {
		root = ws.root
	}
	var id widget.TerminalTabID
	id, err = panel.Add(shellLabel(panel), TerminalConfig{Dir: termDir(root),
		Exited: func(code int) { t.shellExited(id, code) }})
	if err != nil {
		t.c.Notify("the terminal could not add a shell: " + err.Error())
		return
	}
	if err := panel.Start(id); err != nil {
		t.c.Notify("the terminal could not start (" + err.Error() + "): SPC ` or Enter in it tries again")
	}
	t.c.Keep(panel.Select(id))
}

// shellLabel names a new shell's tab: shell, then shell 2, shell 3, …, the first not on panel.
func shellLabel(panel TerminalPanel) string {
	var labels []string
	for _, tab := range panel.Tabs() {
		labels = append(labels, tab.Label)
	}
	for n := 1; ; n++ {
		label := "shell"
		if n > 1 {
			label += " " + strconv.Itoa(n)
		}
		if !slices.Contains(labels, label) {
			return label
		}
	}
}

// tabRows are a shell tab's menu: Go to ›, New, Close.
func (t *terminals) tabRows(id widget.TerminalTabID) []widget.MenuItemModel {
	panel, err := t.panel()
	if err != nil {
		return nil
	}
	closing := tabMenuRow("terminal.close", "Close", t.closeShell)
	closing.Enabled = id != "" // an empty panel's menu has no shell to close
	rows := []widget.MenuItemModel{
		goToRows(panel, func(tab widget.TerminalTab) string { return tab.Label }, func(target widget.TerminalTabID) {
			t.c.Keep(panel.Select(target))
		}),
		tabMenuRow("terminal.new", "New", func(widget.TerminalTabID) { t.newShell() }),
		closing,
	}
	// Hide is SPC `'s own toggle (terminal.toggle): the panel closed, every shell running on
	return append(rows, panelRows(t.c, "terminal", t.toggleTerminal)...)
}

// closeShell is Close on a tab's menu: the shell hung up and its tab closed. The panel stays open,
// on its next shell or its empty text; SPC ` then starts a fresh one.
func (t *terminals) closeShell(id widget.TerminalTabID) {
	panel, err := t.panel()
	if err != nil {
		return
	}
	active := panel.Active()
	if _, err := panel.Remove(id); err != nil {
		t.c.Notify("the shell's tab could not close: " + err.Error())
		return
	}
	t.replaceActive(panel, id, active)
}

// replaceActive follows tab id's removal: the shell that was active stays so, and only when id
// was the active one does the keyboard go to the next live shell. It reports whether a live shell
// is left.
func (t *terminals) replaceActive(panel TerminalPanel, id, active widget.TerminalTabID) bool {
	if id != active {
		if keep := liveShell(panel, active); keep == active {
			return true
		}
	}
	next := liveShell(panel, "")
	if next == "" || next == id {
		return false
	}
	t.c.Keep(panel.Select(next))
	return true
}

// closeTerminalPanel is the terminal's spec Close: it remembers whether the terminal had the
// keyboard, for the close to give it back, and closes the drawer.
func (t *terminals) closeTerminalPanel() {
	focused, err := t.c.Focused("terminalView")
	t.c.Keep(err)
	t.restore = focused
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
		if panel, err := t.panel(); err == nil {
			if id := liveShell(panel, panel.Active()); id != "" {
				t.c.Keep(panel.Select(id))
				return
			}
		}
		t.newShell()
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
	t.c.Keep(t.c.RestoreKeyboardPane(target))
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

// shellExited is a shell gone (exit, Ctrl+D, or it ended): its tab closes, and if it was the
// active one the keyboard goes to the next shell; a background shell's exit moves nothing. The last one gone, the panel closes as SPC ` closes it, its geometry kept and
// the keyboard back where it was, and the next open starts a fresh shell. A non-zero exit is kept
// as a notice, as it may be an error. golib's Terminal says it in an App.Update, where closing the
// drawer may move the keyboard.
func (t *terminals) shellExited(id widget.TerminalTabID, code int) {
	if code != 0 {
		t.c.Notify("the terminal's shell exited (" + strconv.Itoa(code) + ")")
	}
	panel, err := t.panel()
	if err != nil {
		return
	}
	active := panel.Active()
	if _, err := panel.Remove(id); err != nil {
		return // the tab of a parent rebuilt since, or already closing
	}
	if t.replaceActive(panel, id, active) {
		return
	}
	if t.c.PanelOpen("terminal") {
		t.c.TogglePanel("terminal")
	}
}
