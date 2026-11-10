package tui

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/userdirs"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/errs"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
	"github.com/yongjohnlee80/golib/tui/pty"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// terminals owns shell startup and keyboard restoration. The host contributes
// only its PanelHost services; none of this state is shared between programs.
type terminals struct {
	c       feature.PanelHost
	restore bool
	// startLua is a Lua shell's program (Host.startLuaShell, luashell.go); nil: no Lua shells
	startLua func(pty.Cmd) (terminal.TerminalProcess, error)
}

// terminalRuntimeHost is the PanelHost with the terminal panel's tabs (feature/terminalpanel.go).
type terminalRuntimeHost interface {
	feature.PanelHost
	Terminals(string) (feature.TerminalPanel, error)
}

func newTerminals(c feature.PanelHost) *terminals {
	return &terminals{c: c, restore: true}
}

// inOwnEnv is config to run in the program's environment: a tab that names no program starts the
// interactive shell (internal/usershell: $SHELL on Unix, PowerShell on Windows), and, when a test
// gave the program an environment of its own, its variables go under the tab's own (golib's
// terminal otherwise starts with the process's environment under the tab's).
func (t *terminals) inOwnEnv(config feature.TerminalConfig) feature.TerminalConfig {
	env := t.c.Env()
	if config.Command == "" && config.Start == nil {
		sh := usershell.Interactive(env)
		config.Command, config.Args = sh[0], sh[1:]
	}
	if env == nil {
		return config
	}
	config.Env = append(env.Environ(), config.Env...)
	return config
}
func (t *terminals) BindPanelHost(c feature.PanelHost) error {
	t.c = c
	return nil
}
func (*terminals) Name() string { return "terminals" }
func (t *terminals) State() map[string]any {
	st := map[string]any{
		"App.terminalShown":   false,
		"App.terminalEdges":   feature.Choices(termEdgeLabels...),
		"App.terminalSizes":   feature.Choices(percentLabels(termSizes)...),
		"App.terminalLengths": feature.Choices(percentLabels(termLengths)...),
		"App.terminalThemes":  t.themeChooser(),
	}
	maps.Copy(st, t.lookState())
	return st
}
func (t *terminals) Prefs() []feature.PrefSpec { return terminalPrefRows(t) }

func (t *terminals) Catalog() []feature.Entry {
	open := feature.Builtin("terminal.open", "open the terminal", "App.openTerminal()", t.openTerminal)
	open.Verb, open.Aliases = "terminal", []string{"term"}
	open.Help = "open the terminal (SPC `)"
	return []feature.Entry{
		feature.Builtin("terminal.toggle", "the terminal", "App.toggleTerminal()", t.toggleTerminal),
		open,
	}
}

func terminalPrefRows(t *terminals) []feature.PrefSpec {
	row := choicePref(prefTermEdge, "bottom", termEdges, func(p *prefs) *string { return &p.termEdge }).
		Shows(terminalPreferenceState)
	names := termThemes()
	if t != nil {
		names = t.themeNames()
	}
	theme := choicePref(feature.PrefTermTheme, "page", names, func(p *prefs) *string { return &p.termTheme }).
		Shows(func(p feature.PrefsView) map[string]any {
			return map[string]any{"App.terminalThemeIndex": max(indexOf(names, cmp.Or(p.Value(feature.PrefTermTheme), "page")), 0)}
		})
	if t != nil {
		row = row.Then(func(feature.PrefsView) { t.c.DockPanel("terminal") })
		theme = theme.Then(func(feature.PrefsView) { t.showLook() })
	}
	return []feature.PrefSpec{row, theme, panelKeysRow()}
}

// PANEL KEYS WHILE TYPING (v0.3.1) — Ctrl+h/j/k/l move between panels from a terminal's or an
// agent's input mode too. Their keys, as the map binds App.movePane, are the panels' host keys
// (App.terminalHostKeys, feature/keys.go), so the program never sees them; Ctrl+\ then one sends it to the
// program (golib's escape prefix). Options › Terminal turns it off, and they are the program's again.
// Arriving at either panel by one of them puts its tab in input mode, ready to type (panels.go).
const prefTermPanelKeys = "tui.terminal.panel_keys"

// panelKeysRow is the preference: on by default; the menu's check follows it.
func panelKeysRow() feature.PrefSpec {
	return boolPref(prefTermPanelKeys, true, func(p *prefs) *bool { return &p.termPanelKeys }).
		Shows(func(p feature.PrefsView) map[string]any {
			return map[string]any{"App.terminalPanelKeys": p.Value(prefTermPanelKeys) == "true"}
		})
}

// THE TERMINALS' THEME — the colours the terminal's and the agents' shells are drawn in, on their
// own: the page's (the default), or one of the themes golib ships, chosen in Preferences. It is
// their default text, background and cursor, and only theirs: the panel's frame and tab strip stay
// the page's, and the minimum contrast raises a program's colours against the theme's background.
// A program's own 16 colours (ls's, a prompt's) stay the program's: no theme recolours them.

// termThemes are the choices, "page" first.
func termThemes() []string {
	names, _ := feature.ThemeChoices(nil, []string{"page"}, nil)
	return names
}

func (t *terminals) themeNames() []string {
	names, _ := feature.ThemeChoices(t.c, []string{"page"}, nil)
	return names
}

// themeChooser is the theme chooser's rows: the page's, in the UI's language, then every theme.
func (t *terminals) themeChooser() *tuidecl.ListModel {
	return feature.KeyedChoices(feature.ThemeChoices(t.c, []string{"page"}, []string{t.c.Tr("autodoc.text.terminal.the_page_s")}))
}

// LanguageChanged composes the theme chooser's rows again, in the UI's language.
func (t *terminals) LanguageChanged() { t.c.Set("App.terminalThemes", t.themeChooser()) }

// termLook is the theme the terminals are drawn in for p on a page drawn in page: its document's
// text, background and cursor colours. One golib does not ship falls back to the page's, and a page
// theme it does not ship (a layout of one's own) to its first.
func termLook(p feature.PrefsView, page string) map[string]any {
	name := feature.TermThemeOf(p)
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
	var p feature.PrefsView
	if t.c != nil {
		p = t.c.Prefs()
	}
	if c, ok := t.c.(interface {
		ThemeValues(string) (map[string]string, error)
	}); ok {
		if v, ok := feature.TerminalThemeValues(p, page, c.ThemeValues); ok {
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

func terminalPreferenceState(p feature.PrefsView) map[string]any {
	st := termState(p)
	size, length := p.PanelGeometry("terminal", p.Value(prefTermEdge))
	maps.Copy(st, map[string]any{
		"App.terminalEdgeIndex":   indexOf(termEdges, p.Value(prefTermEdge)),
		"App.terminalSizeIndex":   nearest(termSizes, size),
		"App.terminalLengthIndex": nearest(termLengths, length),
	})
	return st
}
func (t *terminals) Commands() map[string]feature.Command {
	return map[string]feature.Command{
		"App.toggleTerminal":    feature.None(t.toggleTerminal),
		"App.stopAllShells":     feature.None(t.stopAllShells),
		"App.openTerminal":      feature.None(t.openTerminal),
		"App.setTerminalEdge":   feature.OneNumber("a row", t.setTerminalEdge),
		"App.setTerminalSize":   feature.OneNumber("a row", t.setTerminalSize),
		"App.setTerminalLength": feature.OneNumber("a row", t.setTerminalLength),
		"App.setTerminalTheme":  feature.OneNumber("a row", t.setTerminalTheme),
	}
}
func (t *terminals) Panels() []feature.PanelSpec {
	s := terminalPanelSpec()
	s.Open = func(feature.PanelHost) { t.openTerminalPanel() }
	s.Close = func(feature.PanelHost) { t.closeTerminalPanel() }
	s.Opened = func(c feature.PanelHost) { c.Set("App.terminalShown", true) }
	s.Closed = func(feature.PanelHost) { t.terminalPanelClosed() }
	return []feature.PanelSpec{s}
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
	termSizes   = feature.Percents(10, 90, 10)
	termLengths = feature.Percents(10, 100, 10)
)

// percentLabels are the choices as the dialog shows them.
func percentLabels(ps []int) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = strconv.Itoa(p) + "%"
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

// termState is what the document reads of the terminal's preferences.
func termState(p feature.PrefsView) map[string]any {
	size, length := p.PanelGeometry("terminal", p.Value(prefTermEdge))
	return map[string]any{
		"App.terminalEdge":   p.Value(prefTermEdge),
		"App.terminalSize":   size,
		"App.terminalLength": length,
	}
}

// syncTermDialog sets the Preferences dialog's terminal choosers.
func (t *terminals) syncTermDialog() {
	p := t.c.Prefs()
	size, length := p.PanelGeometry("terminal", p.Value(prefTermEdge))
	t.c.Set("App.terminalEdgeIndex", indexOf(termEdges, p.Value(prefTermEdge)))
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
		t.c.SetPref(feature.PrefTermTheme, all[i])
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

// openTerminalPanel is the terminal's spec Open (panels.go): it opens the drawer, starts a shell
// when none runs, and gives the shell the keyboard. SPC ` and Go › Terminal are togglePanel, which comes here.
func (t *terminals) openTerminalPanel() {
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

// panel is the terminal's tabs, its strip's + and menu given (feature/tabmenus.go): a rebuilt parent has
// none of the old one's, so every lookup gives them again.
func (t *terminals) panel() (feature.TerminalPanel, error) {
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

// newLuaShell adds a Lua shell tab ("lua", then "lua 2", …), starts it and gives it the keyboard:
// :lua alone, and Lua shell on the strip's + menu. The panel opens when it is closed.
func (t *terminals) newLuaShell() {
	if t.startLua == nil {
		t.c.Notify(errNoShell.Error())
		return
	}
	if !t.c.PanelOpen("terminal") {
		t.c.Keep(t.c.CallView("terminal", "open"))
	}
	t.startTab(luaShellLabel, feature.TerminalConfig{Command: "lua", Start: t.startLua},
		func(err error) string {
			return t.c.Trf("autodoc.notice.terminal.the_terminal_could_not_add_a_lua_shell", err.Error())
		},
		func(err error) string {
			return t.c.Trf("autodoc.notice.terminal.the_lua_shell_could_not_start", err.Error())
		})
}

// liveShell is a tab of panel neither closing nor failed to start: prefer if it is one, else the
// first; "" for none.
func liveShell(panel feature.TerminalPanel, prefer terminal.TerminalTabID) terminal.TerminalTabID {
	first := terminal.TerminalTabID("")
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
	root := ""
	if ws, ok := t.c.ActiveWorkspace(); ok {
		root = ws.Root
	}
	t.startTab(shellLabel, t.inOwnEnv(feature.TerminalConfig{Dir: termDir(t.c.Env(), root)}),
		func(err error) string {
			return t.c.Trf("autodoc.notice.terminal.the_terminal_could_not_add_a_shell", err.Error())
		},
		func(err error) string {
			return t.c.Trf("autodoc.notice.terminal.the_terminal_could_not_start_spc_or_enter_in_it", err.Error())
		})
}

// startTab adds a tab named by label running config, starts it and gives it the keyboard (golib's
// Select gives the tab's terminal the keyboard): a shell's and a Lua shell's. A tab that cannot be
// added is the notice addFail makes of the error; one that fails to start says so, in its tab and in
// the notice startFail makes.
func (t *terminals) startTab(label func(feature.TerminalPanel) string, config feature.TerminalConfig, addFail, startFail func(error) string) {
	panel, err := t.panel()
	if err != nil {
		t.c.Keep(err)
		return
	}
	var id terminal.TerminalTabID
	config.Exited = func(code int) { t.shellExited(id, code) }
	id, err = panel.Add(label(panel), config)
	if err != nil {
		t.c.Notify(addFail(err))
		return
	}
	if err := panel.Start(id); err != nil {
		t.c.Notify(startFail(err))
	}
	t.c.Keep(panel.Select(id))
}

// shellLabel names a new shell's tab: shell, then shell 2, shell 3, …, the first not on panel.
func shellLabel(panel feature.TerminalPanel) string {
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

// tabRows are a shell tab's menu: the panel's Layout ›, Go to ›, New, Close.
func (t *terminals) tabRows(id terminal.TerminalTabID) []widgetcore.MenuItemModel {
	panel, err := t.panel()
	if err != nil {
		return nil
	}
	closing := feature.TabMenuRow("terminal.close", "Close", t.closeShell)
	closing.Enabled = id != "" // an empty panel's menu has no shell to close
	rows := append(layoutRows(t.c, "terminal", feature.TabRowOf),
		feature.GoToRows(panel, func(tab terminal.TerminalTab) string { return tab.Label }, func(target terminal.TerminalTabID) {
			t.c.Keep(panel.Select(target))
		}),
		feature.TabMenuRow("terminal.new", "New", func(terminal.TerminalTabID) { t.newShell() }))
	if t.startLua != nil {
		rows = append(rows, feature.TabMenuRow("terminal.lua", "New &Lua shell", func(terminal.TerminalTabID) { t.newLuaShell() }))
	}
	rows = append(rows, closing)
	// Hide is SPC `'s own toggle (terminal.toggle): the panel closed, every shell running on
	return append(rows, feature.PanelRows(t.c, "terminal", panel, id, t.toggleTerminal)...)
}

// closeShell is Close on a tab's menu: the shell hung up and its tab closed. The panel stays open,
// on its next shell or its empty text; SPC ` then starts a fresh one.
func (t *terminals) closeShell(id terminal.TerminalTabID) {
	panel, err := t.panel()
	if err != nil {
		return
	}
	active := panel.Active()
	if _, err := panel.Remove(id); err != nil {
		t.c.Notify(t.c.Trf("autodoc.notice.terminal.the_shell_s_tab_could_not_close", err.Error()))
		return
	}
	t.replaceActive(panel, id, active)
}

// stopAllShells is the strip's x: every shell in the panel hung up and its tab closed, then the panel
// closed; asked once while any of them runs. _ beside it is the panel's own hide, every shell kept.
func (t *terminals) stopAllShells() {
	panel, err := t.panel()
	if err != nil {
		return
	}
	running := 0
	for _, tab := range panel.Tabs() {
		if tab.Running && !tab.Closing {
			running++
		}
	}
	stopAll := func() {
		for _, tab := range panel.Tabs() { // a tab already closing answers its removal as before
			if _, err := panel.Remove(tab.ID); err != nil {
				t.c.Notify(t.c.Trf("autodoc.notice.terminal.the_shell_s_tab_could_not_close", err.Error()))
			}
		}
		t.closeTerminalPanel()
	}
	if running == 0 {
		stopAll()
		return
	}
	t.c.Ask(feature.Question{Title: t.c.Tr("autodoc.question.terminal.stop_the_shells_and_close_the_terminal"),
		Text: t.c.Trf("autodoc.question.terminal.stop_every_shell_running_here", running),
		Yes:  t.c.Tr("autodoc.question.terminal.yes_stop_them"), OnYes: stopAll})
}

// replaceActive follows tab id's removal: the shell that was active stays so, and only when id
// was the active one does the keyboard go to the next live shell. It reports whether a live shell
// is left.
func (t *terminals) replaceActive(panel feature.TerminalPanel, id, active terminal.TerminalTabID) bool {
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
// (Escape inside it, SPC ` from it), and the keyboard goes to the topmost window still open.
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

// termDir is where the shell starts: the workspace's folder when this machine has it as a folder
// (a daemon may serve a root only it can see), else the user's home (env's, nil for AutoDoc's own).
func termDir(env procenv.Env, root string) string {
	if fi, err := os.Stat(root); err == nil && fi.IsDir() {
		return root
	}
	if home, err := userdirs.Home(env); err == nil {
		return home
	}
	return ""
}

// terminalClosed gives the keyboard to the topmost window still open, as every closed panel's goes
// (RestoreKeyboardHome), when the terminal had it as it closed (Escape inside it, or SPC ` from it).
func (t *terminals) terminalClosed() {
	restore := t.restore
	t.restore = true // Escape inside the terminal closes it without passing toggleTerminal
	if !restore {
		return
	}
	t.c.Keep(t.c.RestoreKeyboardHome())
}

// keyboardPanes are the panes the keyboard can come back to from the terminal: the page, then the
// panels that take it back.
func keyboardPanes(specs []feature.PanelSpec) []string {
	out := []string{"editor"}
	for _, s := range specs {
		if s.Keyboard {
			out = append(out, s.View)
			if v := s.FocusView(); v != s.View { // a tabbed panel's other tab shown
				out = append(out, v)
			}
		}
	}
	return out
}

func (h *Host) paneHasKeyboard(id string) bool {
	c, ok := h.p.Find(id)
	return ok && h.p.App().FocusWithin(c)
}

// shellExited is a shell gone (exit, Ctrl+D, or it ended): its tab closes, and if it was the
// active one the keyboard goes to the next shell; a background shell's exit moves nothing. The last one gone, the panel closes as SPC ` closes it, its geometry kept and
// the keyboard to the topmost window still open, and the next open starts a fresh shell. A non-zero exit is kept
// as a notice, as it may be an error. golib's Terminal says it in an App.Update, where closing the
// drawer may move the keyboard.
func (t *terminals) shellExited(id terminal.TerminalTabID, code int) {
	if code != 0 {
		t.c.Notify(t.c.Trf("autodoc.notice.terminal.the_terminal_s_shell_exited", strconv.Itoa(code)))
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

func terminalPanelSpec() feature.PanelSpec {
	return feature.PanelSpec{ID: "terminal", View: "terminalView", Priority: 30, Icon: "❯",
		Edge: func(p feature.PrefsView) string { return p.Value(prefTermEdge) }, Places: slices.Clone(termEdges),
		Size: [2]int{10, 90}, Length: [2]int{10, 100}, Default: termDefault}
}
