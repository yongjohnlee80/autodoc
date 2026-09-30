package tui

import (
	"context"
	"strconv"
	"strings"
)

// PREFERENCES — what the TUI keeps between runs, in the daemon's store (preference.list and
// preference.set), so they are the same whichever workspace is open: the editor's keymap (Vim, or
// Text: modeless), the theme, whether the menu bar hides, whether the status line shows, the edge
// each panel opens from, and the page's width. Options › Editor preferences… sets them; the AI
// models are their own dialog, under System (providers.go).

// The TUI's preferences, by the names the store keeps them under.
const (
	prefTheme    = "tui.theme"
	prefMenuHide = "tui.menu.autohide"
	prefStatus   = "tui.status.shown"
	prefExplorer = "tui.explorer.edge"
	prefLinks    = "tui.links.edge"
	prefRuler    = "tui.ruler"
	prefKeymap   = "tui.editor.keymap"
)

// The keymaps, by the names the store keeps them under, and the editor's keysets for them: Vim is
// modal; Text is an ordinary text editor's keys, modeless.
var keymaps = []string{"vim", "text"}

var keysetOf = map[string]string{"vim": "vim", "text": "standard"}

// keymapLabels are the keymaps as the choosers and the menu offer them.
var keymapLabels = []string{"Vim (modal)", "Text (modeless)"}

// The defaults: a blank page, the menu hidden until F10 or an Alt+letter, the explorer on the
// left, the links on the right, a page 120 columns wide.
const (
	defaultTheme = "dark"
	defaultRuler = 120
	minRuler     = 40
	maxRuler     = 400
)

// prefs are the preferences as the TUI uses them.
type prefs struct {
	theme                  string
	menuHidden, statusOn   bool
	explorerEdge, linkEdge string
	ruler                  int
	keymap                 string
}

func defaultPrefs() prefs {
	return prefs{theme: defaultTheme, menuHidden: true, explorerEdge: "left", linkEdge: "right", ruler: defaultRuler, keymap: "vim"}
}

// edges are the four a panel opens from, in the order the Preferences dialog offers them.
var edges = []string{"left", "right", "top", "bottom"}

func isEdge(s string) bool {
	for _, e := range edges {
		if e == s {
			return true
		}
	}
	return false
}

// prefsOf reads the store's preferences over the defaults; a value the TUI cannot use is the
// default's.
func prefsOf(m map[string]any) prefs {
	p := defaultPrefs()
	str := func(k string) (string, bool) { s, ok := m[k].(string); return s, ok && s != "" }
	if s, ok := str(prefTheme); ok {
		p.theme = s
	}
	if s, ok := str(prefMenuHide); ok {
		p.menuHidden = s != "false"
	}
	if s, ok := str(prefStatus); ok {
		p.statusOn = s == "true"
	}
	if s, ok := str(prefExplorer); ok && isEdge(s) {
		p.explorerEdge = s
	}
	if s, ok := str(prefLinks); ok && isEdge(s) {
		p.linkEdge = s
	}
	if s, ok := str(prefKeymap); ok && keysetOf[s] != "" {
		p.keymap = s
	}
	if s, ok := str(prefRuler); ok {
		if n, err := strconv.Atoi(s); err == nil && n >= minRuler && n <= maxRuler {
			p.ruler = n
		}
	}
	return p
}

// panelLength is a side panel's share of the Window along its edge, centred: a panel at the left
// or right takes 85% of the rows, one at the top or bottom 80% of the columns, so the page shows
// around it rather than the panel filling the edge end to end.
func panelLength(edge string) int {
	if edge == "top" || edge == "bottom" {
		return 80
	}
	return 85
}

// prefState is what the document reads of the preferences (the status line's is statusShown's).
func prefState(p prefs) map[string]any {
	return map[string]any{
		"App.menuAutoHide":   p.menuHidden,
		"App.keyset":         keysetOf[p.keymap],
		"App.keymapVim":      p.keymap == "vim",
		"App.keymapText":     p.keymap == "text",
		"App.explorerEdge":   p.explorerEdge,
		"App.linksEdge":      p.linkEdge,
		"App.explorerLength": panelLength(p.explorerEdge),
		"App.linksLength":    panelLength(p.linkEdge),
		// the page: the ruler's columns of text, and its border, whose right edge is the first
		// column past them (vim's colorcolumn at textwidth+1); the editor's guide marks that
		// column too, so a line scrolled past the page's edge still shows where it is
		"App.pageWidth":   p.ruler + 2,
		"App.rulerColumn": p.ruler + 1,
		"App.rulerText":   strconv.Itoa(p.ruler),
	}
}

// loadPrefs reads the store's preferences on a connection, and applies them: the theme by a
// reload, when it is not the one on screen.
func (h *Host) loadPrefs() {
	gen := h.session.Gen() // daemon-wide: a workspace switch keeps it, a new connection drops it
	type answer struct {
		m   map[string]any
		err error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "preference.list")
		return answer{asMap(res), err}
	}, func(a answer) {
		if gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			h.failed("preferences", a.err)
			return
		}
		h.applyPrefs(prefsOf(a.m))
	})
}

func (h *Host) applyPrefs(p prefs) {
	h.prefs = p
	for k, v := range prefState(p) {
		h.set(k, v)
	}
	h.set("App.statusShown", h.statusShown())
	h.syncPrefDialog()
	if p.theme != h.theme {
		h.switchTheme(p.theme)
	}
}

// statusShown is whether the status line shows: when its preference says, and while the TUI is
// not connected, whatever it says, so a connection that fails is never silent.
func (h *Host) statusShown() bool { return h.prefs.statusOn || !h.connected }

// setConnected says whether the TUI is connected, for the status line to show while it is not.
func (h *Host) setConnected(v bool) {
	h.connected = v
	h.set("App.statusShown", h.statusShown())
	if !v {
		// no daemon, no search: the mark returns with the first status after connecting
		h.set("App.semanticMark", "")
		h.set("App.semanticLabel", "")
	}
}

// setPref changes one preference: on screen at once, and in the store.
func (h *Host) setPref(name, value string, change func(*prefs)) {
	p := h.prefs
	change(&p)
	h.applyPrefs(p)
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "preference.set", name, value)
		return err
	}, func(err error) {
		if err != nil {
			h.failed("save the preference", err)
		}
	})
}

// The preference commands, as the menu, the leader and the Preferences dialog give them.

func (h *Host) toggleMenuBar() {
	v := !h.prefs.menuHidden
	h.setPref(prefMenuHide, strconv.FormatBool(v), func(p *prefs) { p.menuHidden = v })
	if v {
		h.setStatus("the menu bar hides: F10 or Alt+letter brings it up")
	} else {
		h.setStatus("the menu bar shows")
	}
}

func (h *Host) toggleStatusLine() {
	v := !h.prefs.statusOn
	h.setPref(prefStatus, strconv.FormatBool(v), func(p *prefs) { p.statusOn = v })
}

func (h *Host) setExplorerEdge(i int) {
	if i >= 0 && i < len(edges) {
		e := edges[i]
		h.setPref(prefExplorer, e, func(p *prefs) { p.explorerEdge = e })
	}
}

func (h *Host) setLinksEdge(i int) {
	if i >= 0 && i < len(edges) {
		e := edges[i]
		h.setPref(prefLinks, e, func(p *prefs) { p.linkEdge = e })
	}
}

// setRuler takes the page's width from the Preferences dialog's field, as it is typed: a number of
// columns in range applies at once; another says what the field takes, and changes nothing.
func (h *Host) setRuler(text string) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < minRuler || n > maxRuler {
		h.set("App.prefsError", "the page's width is a number of columns, "+strconv.Itoa(minRuler)+" to "+strconv.Itoa(maxRuler))
		return
	}
	h.set("App.prefsError", "")
	h.setPref(prefRuler, strconv.Itoa(n), func(p *prefs) { p.ruler = n })
}

// useTheme is View › Theme and the Preferences dialog: the theme on screen, and kept.
func (h *Host) useTheme(name string) {
	if !isTheme(name) {
		h.setStatus("no theme " + strconv.Quote(name))
		return
	}
	h.setPref(prefTheme, name, func(p *prefs) { p.theme = name })
}

func (h *Host) setThemeIndex(i int) {
	if i >= 0 && i < len(themeNames) {
		h.useTheme(themeNames[i])
	}
}

// setKeymap switches the editor's keymap, and keeps it.
func (h *Host) setKeymap(name string) {
	if keysetOf[name] == "" {
		h.setStatus("no keymap " + strconv.Quote(name))
		return
	}
	h.setPref(prefKeymap, name, func(p *prefs) { p.keymap = name })
	h.setStatus("keymap: " + keymapLabels[indexOf(keymaps, name)])
}

func (h *Host) setKeymapIndex(i int) {
	if i >= 0 && i < len(keymaps) {
		h.setKeymap(keymaps[i])
	}
}

// toggleKeymap is SPC k: Vim, or Text.
func (h *Host) toggleKeymap() {
	if h.prefs.keymap == "vim" {
		h.setKeymap("text")
	} else {
		h.setKeymap("vim")
	}
}

// openPrefs opens the editor's preferences as they are.
func (h *Host) openPrefs() {
	h.set("App.prefsError", "")
	h.syncPrefDialog()
	h.open("preferences")
}

// openAIModels opens the AI models: the providers on the left, the one under the cursor's usage
// and calls on the right.
func (h *Host) openAIModels() {
	h.loadProviders()
	h.open("aiModels")
}

// syncPrefDialog sets the dialog's choosers to the preferences.
func (h *Host) syncPrefDialog() {
	h.set("App.themeIndex", indexOf(themeNames, h.prefs.theme))
	h.set("App.keymapIndex", indexOf(keymaps, h.prefs.keymap))
	h.set("App.explorerEdgeIndex", indexOf(edges, h.prefs.explorerEdge))
	h.set("App.linksEdgeIndex", indexOf(edges, h.prefs.linkEdge))
	h.set("App.menuHiddenIndex", boolIndex(h.prefs.menuHidden))
	h.set("App.statusShownIndex", boolIndex(h.prefs.statusOn))
}

func (h *Host) setMenuHiddenIndex(i int) {
	if v := i == 0; v != h.prefs.menuHidden {
		h.toggleMenuBar()
	}
}

func (h *Host) setStatusShownIndex(i int) {
	if v := i == 0; v != h.prefs.statusOn {
		h.toggleStatusLine()
	}
}

// boolIndex is a yes/no chooser's row: yes first.
func boolIndex(v bool) int {
	if v {
		return 0
	}
	return 1
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return 0
}
