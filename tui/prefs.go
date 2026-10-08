package tui

import (
	"context"
	"slices"
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
	prefTheme = "tui.theme"
	// the GUI's theme, kept apart from the terminal's: a light page in a window, a dark one among
	// the terminal's tools, each kept as chosen
	prefGUITheme = "gui.theme"
	prefMenuHide = "tui.menu.autohide"
	prefStatus   = "tui.status.shown"
	prefExplorer = "tui.explorer.edge"
	prefLinks    = "tui.links.edge"
	prefRuler    = "tui.ruler"
	prefKeymap   = "tui.editor.keymap"
	prefWrap     = "tui.editor.wrap"
	prefNumbers  = "tui.editor.linenumbers"
	prefImages   = "tui.preview.images"
	prefCorner   = "tui.toast.corner"
	prefSeconds  = "tui.toast.seconds"
	// a plugin's placement: tui.plugin.<name>.placement
	prefPluginPrefix = "tui.plugin."
	prefPluginPlace  = ".placement"
	// a plugin command's SPC p letter: tui.plugin.<name>.key.<id>
	prefPluginKey = ".key."
)

// corners are where the toasts can stack, in the order the Preferences dialog offers them.
var corners = []string{"bottom-right", "bottom-left", "top-right", "top-left"}

var cornerLabels = []string{"bottom right", "bottom left", "top right", "top left"}

// How long a finished toast stays, in seconds: the default, and the range the dialog offers.
const (
	defaultToastSeconds = 3
	maxToastSeconds     = 10
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
	defaultTheme    = "dark"
	defaultGUITheme = "sepia"
	defaultRuler    = 120
	minRuler        = 40
	maxRuler        = 400
)

// prefs are the preferences as the TUI uses them.
type prefs struct {
	theme                  string
	menuHidden, statusOn   bool
	explorerEdge, linkEdge string
	ruler                  int
	keymap                 string
	wrap, lineNumbers      bool   // the page: long lines wrapped at its width; each line's number
	images                 bool   // previews as images where the terminal draws them (preview.go)
	toastCorner            string // where the notifications stack
	toastSeconds           int    // how long a finished one stays
	// the terminal: the edge it opens from, and each edge's size and length the user set
	termEdge             string
	termSize, termLength map[string]int
	// pluginPlace is each plugin's placement the user chose, by name (tui.plugin.<name>.placement);
	// one its manifest does not offer is ignored where it is read (plugins.go placement)
	pluginPlace map[string]string
	// pluginKeys are the SPC p letters the user chose, by "<plugin>.<command id>"
	// (tui.plugin.<name>.key.<id>); "" unbinds the command (plugincmds.go)
	pluginKeys map[string]string
	// searchStages are the search's boxes as the user left them (stages.go)
	searchStages stageChoice
	// panelGeo are the explorer's and the links' dragged sizes, by preference name (panelsize.go)
	panelGeo map[string]int
	// panelFloat are the panels moved or resized off their edges: x, y, w, h in percent of the
	// Window (panelfloat.go)
	panelFloat map[string][4]int
	// agents are the agent terminal's profiles, and agentDefault the one SPC g opens (agent.go)
	agents       []agentProfile
	agentDefault string
	// agentEdge is where the agent panel opens: an edge or the centre (agentplace.go)
	agentEdge string
	// recent are the files opened last, newest first (recent.go)
	recent []recentDoc
}

func defaultPrefs() prefs { return defaultPrefsFor(false) }

// defaultPrefsFor are the defaults of the terminal's UI, or of the GUI's: they differ in the
// theme alone.
func defaultPrefsFor(gui bool) prefs {
	theme := defaultTheme
	if gui {
		theme = defaultGUITheme
	}
	return prefs{theme: theme, menuHidden: true, explorerEdge: "left", linkEdge: "right", ruler: defaultRuler, keymap: "vim",
		wrap: true, images: true, toastCorner: "bottom-right", toastSeconds: defaultToastSeconds,
		termEdge: "bottom", termSize: map[string]int{}, termLength: map[string]int{}, searchStages: allStagesChecked(),
		agentEdge: defaultAgentEdge}
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
func prefsOf(m map[string]any) prefs { return prefsFor(m, false) }

// prefsFor reads the store's preferences for the terminal's UI or the GUI's: the same but the
// theme, each its own (themePref).
func prefsFor(m map[string]any, gui bool) prefs {
	p := defaultPrefsFor(gui)
	str := func(k string) (string, bool) { s, ok := m[k].(string); return s, ok && s != "" }
	if s, ok := str(themePref(gui)); ok {
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
	if s, ok := str(prefWrap); ok {
		p.wrap = s != "false"
	}
	if s, ok := str(prefNumbers); ok {
		p.lineNumbers = s == "true"
	}
	if s, ok := str(prefImages); ok {
		p.images = s != "false"
	}
	if s, ok := str(prefCorner); ok && slices.Contains(corners, s) {
		p.toastCorner = s
	}
	if s, ok := str(prefSearchStages); ok {
		p.searchStages = stageChoiceOf(s)
	}
	if s, ok := str(prefSeconds); ok {
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= maxToastSeconds {
			p.toastSeconds = n
		}
	}
	for k, v := range m {
		if name, ok := strings.CutPrefix(k, prefPluginPrefix); ok {
			if plug, id, ok := strings.Cut(name, prefPluginKey); ok {
				if s, isStr := v.(string); isStr {
					if p.pluginKeys == nil {
						p.pluginKeys = map[string]string{}
					}
					p.pluginKeys[plug+"."+id] = s
				}
				continue
			}
			if name, ok = strings.CutSuffix(name, prefPluginPlace); ok {
				if s, _ := v.(string); s != "" {
					if p.pluginPlace == nil {
						p.pluginPlace = map[string]string{}
					}
					p.pluginPlace[name] = s
				}
			}
		}
	}
	if s, ok := str(prefRuler); ok {
		if n, err := strconv.Atoi(s); err == nil && n >= minRuler && n <= maxRuler {
			p.ruler = n
		}
	}
	readTermPrefs(&p, m)
	readAgentPrefs(&p, m)
	if s, ok := str(prefAgentProfiles); ok {
		p.agents = agentProfilesOf(s)
	}
	if s, ok := str(prefAgentDefault); ok {
		p.agentDefault = s
	}
	if s, ok := str(prefRecent); ok {
		p.recent = recentOf(s)
	}
	readPanelPrefs(&p, m)
	readPanelFloats(&p, m)
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
	m := termState(p)
	for k, v := range panelState(p) {
		m[k] = v
	}
	for k, v := range agentState(p) {
		m[k] = v
	}
	for k, v := range floatState(p.panelFloat) {
		m[k] = v
	}
	for k, v := range map[string]any{
		"App.menuAutoHide":  p.menuHidden,
		"App.keyset":        keysetOf[p.keymap],
		"App.keymapVim":     p.keymap == "vim",
		"App.keymapText":    p.keymap == "text",
		"App.explorerEdge":  p.explorerEdge,
		"App.linksEdge":     p.linkEdge,
		"App.editorWrap":    p.wrap,
		"App.lineNumbers":   p.lineNumbers,
		"App.imagePreviews": p.images,
		// the page: the ruler's columns of text (editable columns: the line numbers' gutter is added
		// to them, syncPageWidth), and its border, whose right edge is the first column past them
		// (vim's colorcolumn at textwidth+1); the editor's guide marks that column too, so a line
		// scrolled past the page's edge still shows where it is
		"App.rulerColumn": p.ruler + 1,
		"App.rulerText":   strconv.Itoa(p.ruler),
	} {
		m[k] = v
	}
	return m
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
		h.applyPrefs(prefsFor(a.m, h.gui))
	})
}

func (h *Host) applyPrefs(p prefs) {
	h.prefs = p
	for k, v := range prefState(p) {
		h.set(k, v)
	}
	h.set("App.statusShown", h.statusShown())
	h.syncPageWidth()
	h.syncPrefDialog()
	h.applyToastPrefs()
	h.syncStages(true)
	h.refreshPlugins() // the SPC p letters the user chose
	if p.theme != h.theme {
		h.switchTheme(p.theme)
	}
}

// syncPageWidth sizes the page to hold the ruler's columns of text: its border, and the line
// numbers' gutter when they show, whose width the editor says (it grows with the file's lines).
func (h *Host) syncPageWidth() {
	w := h.prefs.ruler + 2 + h.gutterWidth()
	if w != h.pageWidth {
		h.pageWidth = w
		h.set("App.pageWidth", w)
	}
}

// statusShown is whether the status line shows: when its preference says, and while the TUI is
// not connected, whatever it says, so a connection that fails is never silent.
func (h *Host) statusShown() bool { return h.prefs.statusOn || !h.connected }

// setConnected says whether the TUI is connected, for the status line to show while it is not.
func (h *Host) setConnected(v bool) {
	h.connected = v
	h.set("App.statusShown", h.statusShown())
	h.applyToastPrefs() // the status line shows or hides: the toasts' margin follows
	if !v {
		// no daemon, no search: the mark returns with the first status after connecting
		h.set("App.semanticMark", "")
		h.set("App.semanticLabel", "")
		h.set("App.semanticDetail", "")
		h.set("App.searchTitle", "search")
	}
}

// setPref changes one preference: on screen at once, and in the store, in the order the changes
// were made. Each write is its own call on its own worker, and the daemon may answer them in any
// order: sent together, an earlier change could land after a later one and the store keep it, so
// the next start shows what the user changed away from. They go one at a time instead.
func (h *Host) setPref(name, value string, change func(*prefs)) {
	p := h.prefs
	change(&p)
	h.applyPrefs(p)
	h.storePref(name, value)
}

// storePref writes one preference to the store, behind those waiting. Alone, it is for one
// nothing on screen shows: the recent files (recent.go), which every file opened changes.
func (h *Host) storePref(name, value string) {
	h.prefWrites = append(h.prefWrites, [2]string{name, value})
	if len(h.prefWrites) == 1 {
		h.writePref()
	}
}

// writePref writes the first preference waiting, then the next, until none waits.
func (h *Host) writePref() {
	w := h.prefWrites[0]
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "preference.set", w[0], w[1])
		return err
	}, func(err error) {
		if err != nil {
			h.failed("save the preference", err)
		}
		h.prefWrites = h.prefWrites[1:]
		if len(h.prefWrites) > 0 {
			h.writePref()
		}
	})
}

// The preference commands, as the menu, the leader and the Preferences dialog give them.

func (h *Host) toggleMenuBar() {
	v := !h.prefs.menuHidden
	h.setPref(prefMenuHide, strconv.FormatBool(v), func(p *prefs) { p.menuHidden = v })
	if v {
		h.say("the menu bar hides: F10 or Alt+letter brings it up")
	} else {
		h.say("the menu bar shows")
	}
}

func (h *Host) leaderMenuBar() {
	h.toggleMenuBar()
	if !h.prefs.menuHidden {
		h.p.Post(func() {
			h.p.Post(func() { h.keep(h.p.Call("menuBar", "forceActiveFocus")) })
		})
	} else {
		h.p.Post(func() { h.keep(h.p.Call("editor", "forceActiveFocus")) })
	}
}

// toggleWrap is View › Wrap long lines: the page's long lines wrapped at its width, or scrolled.
func (h *Host) toggleWrap() {
	v := !h.prefs.wrap
	h.setPref(prefWrap, strconv.FormatBool(v), func(p *prefs) { p.wrap = v })
}

// toggleLineNumbers is View › Line numbers.
func (h *Host) toggleLineNumbers() {
	v := !h.prefs.lineNumbers
	h.setPref(prefNumbers, strconv.FormatBool(v), func(p *prefs) { p.lineNumbers = v })
}

func (h *Host) setWrapIndex(i int) {
	if i == 0 || i == 1 {
		v := i == 0
		h.setPref(prefWrap, strconv.FormatBool(v), func(p *prefs) { p.wrap = v })
	}
}

func (h *Host) setLineNumbersIndex(i int) {
	if i == 0 || i == 1 {
		v := i == 0
		h.setPref(prefNumbers, strconv.FormatBool(v), func(p *prefs) { p.lineNumbers = v })
	}
}

// setToastCorner is the Preferences dialog's corner for the notifications.
func (h *Host) setToastCorner(i int) {
	if i >= 0 && i < len(corners) {
		c := corners[i]
		h.setPref(prefCorner, c, func(p *prefs) { p.toastCorner = c })
	}
}

// setToastSeconds is how long a finished notification stays: row i is i+1 seconds.
func (h *Host) setToastSeconds(i int) {
	if n := i + 1; n >= 1 && n <= maxToastSeconds {
		h.setPref(prefSeconds, strconv.Itoa(n), func(p *prefs) { p.toastSeconds = n })
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
		h.dockPanel("explorer") // a new edge is seen at once
	}
}

func (h *Host) setLinksEdge(i int) {
	if i >= 0 && i < len(edges) {
		e := edges[i]
		h.setPref(prefLinks, e, func(p *prefs) { p.linkEdge = e })
		h.dockPanel("links")
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
		h.say("no theme " + strconv.Quote(name))
		return
	}
	h.setPref(themePref(h.gui), name, func(p *prefs) { p.theme = name })
}

// themePref is the preference the theme is kept under: the GUI's, or the terminal's.
func themePref(gui bool) string {
	if gui {
		return prefGUITheme
	}
	return prefTheme
}

func (h *Host) setThemeIndex(i int) {
	if i >= 0 && i < len(themeNames) {
		h.useTheme(themeNames[i])
	}
}

// setKeymap switches the editor's keymap, and keeps it.
func (h *Host) setKeymap(name string) {
	if keysetOf[name] == "" {
		h.say("no editor mode " + strconv.Quote(name))
		return
	}
	h.setPref(prefKeymap, name, func(p *prefs) { p.keymap = name })
	h.say("editor mode: " + keymapLabels[indexOf(keymaps, name)])
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
	if h.aiTab == rankerTab {
		h.loadRankers()
	}
	h.open("aiModels")
}

// syncPrefDialog sets the dialog's choosers to the preferences.
func (h *Host) syncPrefDialog() {
	h.set("App.themeIndex", indexOf(themeNames, h.prefs.theme))
	h.set("App.keymapIndex", indexOf(keymaps, h.prefs.keymap))
	h.set("App.explorerEdgeIndex", indexOf(edges, h.prefs.explorerEdge))
	h.set("App.linksEdgeIndex", indexOf(edges, h.prefs.linkEdge))
	h.set("App.menuHiddenIndex", boolIndex(h.prefs.menuHidden))
	h.set("App.wrapIndex", boolIndex(h.prefs.wrap))
	h.set("App.lineNumbersIndex", boolIndex(h.prefs.lineNumbers))
	h.set("App.imagesIndex", boolIndex(h.prefs.images))
	h.set("App.toastCornerIndex", indexOf(corners, h.prefs.toastCorner))
	h.set("App.toastSecondsIndex", h.prefs.toastSeconds-1)
	h.set("App.statusShownIndex", boolIndex(h.prefs.statusOn))
	h.syncTermDialog()
	h.syncAgentDialog()
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
