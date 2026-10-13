package tui

import (
	"context"
	"maps"
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
	prefGUITheme      = "gui.theme"
	prefMenuHide      = "tui.menu.autohide"
	prefStatus        = "tui.status.shown"
	prefExplorer      = "tui.explorer.edge"
	prefRelationsEdge = "tui.links.edge"
	prefTodosEdge     = "tui.todos.edge"
	prefRuler         = "tui.ruler"
	prefKeymap        = "tui.editor.keymap"
	prefWrap          = "tui.editor.wrap"
	prefNumbers       = "tui.editor.linenumbers"
	prefIndent        = "tui.editor.indentmarks"
	prefImages        = "tui.preview.images"
	prefOpenAs        = "tui.documents.open"
	prefCorner        = "tui.toast.corner"
	prefAboutAt       = "tui.about.atstart"
	prefUpdates       = "tui.update.check"
	prefSeconds       = "tui.toast.seconds"
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

// The indentation marks, by the names the store keeps them under (the editor's own: off, the
// cursor's line while the editor has the focus, or every line), and as the choosers offer them. A
// line's leading spaces show as a dimmed "·" and its leading tabs as "→", as Vim's listchars do.
var indentMarks = []string{"off", "cursorLine", "all"}

var indentMarkLabels = []string{"off", "on the cursor's line", "on every line"}

// The defaults: a blank page, the menu hidden until F10 or an Alt+letter, the explorer on the
// left, the relations on the right, a page 120 columns wide.
const (
	defaultTheme    = "dark"
	defaultGUITheme = "sepia"
	defaultRuler    = 120
	minRuler        = 40
	maxRuler        = 400
)

// prefs are the preferences as the TUI uses them.
type prefs struct {
	theme                       string
	menuHidden, statusOn        bool
	explorerEdge, relationsEdge string
	todosEdge                   string // where the todos panel opens: the left edge unless chosen
	ruler                       int
	keymap                      string
	wrap, lineNumbers           bool   // the page: long lines wrapped at its width; each line's number
	indentMarks                 string // which lines show their indentation as marks (indentMarks)
	images                      bool   // previews as images where the terminal draws them (preview.go)
	openAs                      string // how a document with two views opens: "prompt", "raw" or "simplified" (files.go)
	aboutAtStart                bool   // About opens when the program starts (startup.go)
	updateCheck                 bool   // a newer release is looked for when the program starts (startup.go)
	toastCorner                 string // where the notifications stack
	toastSeconds                int    // how long a finished one stays
	// the terminal: the edge it opens from; its geometry is in panelGeo
	termEdge string
	// termTheme is the theme the terminals' area is drawn in: "page" (or "") the page's, else one
	// golib ships (terminal.go)
	termTheme string
	// pluginPlace is each plugin's placement the user chose, by name (tui.plugin.<name>.placement);
	// one its manifest does not offer is ignored where it is read (plugins.go placement)
	pluginPlace map[string]string
	// pluginKeys are the SPC p letters the user chose, by "<plugin>.<command id>"
	// (tui.plugin.<name>.key.<id>); "" unbinds the command (plugincmds.go)
	pluginKeys map[string]string
	// searchStages are the search's boxes as the user left them (stages.go)
	searchStages stageChoice
	// panelGeo are every sized panel's kept size and length, by preference name (panelsize.go)
	panelGeo map[string]int
	// panelDefs is this program's validated snapshot. A standalone reader uses
	// fresh built-in descriptors rather than a shared runtime registry.
	panelDefs []PanelSpec
	// panelFloat are the panels moved or resized off their edges: x, y, w, h in percent of the
	// Window (panelfloat.go)
	panelFloat map[string][4]float64
	// the GUI's fonts and zoom (fonts.go): "" and 0 are the window's defaults
	cellFont, proseFont string
	// termFont is the terminals' own face (gui.font.terminal): "" the cell font's (fonts.go)
	termFont       string
	fontSize, zoom int
	// agentEdge is where the agent panel opens: an edge or the centre (agentplace.go)
	agentEdge string
	// values are the features' own preferences (ValuePref), by name: read with Value
	values map[string]string
}

func defaultPrefs() prefs { return defaultPrefsFor(false) }

// defaultPrefsFor are the defaults of the terminal's UI, or of the GUI's: they differ in the
// theme alone.
func defaultPrefsFor(gui bool) prefs {
	p := prefs{searchStages: allStagesChecked()}
	readRows(&p, nil, legacyPrefRows(nil, gui))
	readRows(&p, nil, terminalPrefRows(nil))
	readRows(&p, nil, agentPrefRows(nil))
	return p
}

// legacyPrefRows are the host's own preferences, one row each (prefspec.go): the GUI's theme or
// the terminal's (gui), the default of each, and what a change does on h. Reading the store needs
// no host: h is nil there, and the rows' Changed never runs.
func legacyPrefRows(h *Host, gui bool) []PrefSpec {
	theme := defaultTheme
	if gui {
		theme = defaultGUITheme
	}
	themeRow := choicePref(themePref(gui), theme, themeNames, func(p *prefs) *string { return &p.theme }).
		chooser("App.themeIndex", "App.setThemeIndex")
	themeRow.valid = func(v string) bool { return v != "" } // a theme this build lacks is kept, and the default drawn
	seconds := make([]string, maxToastSeconds)
	for i := range seconds {
		seconds[i] = strconv.Itoa(i + 1)
	}
	toastSeconds := intPref(prefSeconds, defaultToastSeconds, 1, maxToastSeconds, func(p *prefs) *int { return &p.toastSeconds }).
		chooser("App.toastSecondsIndex", "App.setToastSeconds")
	toastSeconds.Choices = seconds
	return []PrefSpec{
		themeRow,
		boolPref(prefMenuHide, true, func(p *prefs) *bool { return &p.menuHidden }).
			chooser("App.menuHiddenIndex", "App.setMenuHiddenIndex").
			shows(func(p prefs) map[string]any { return map[string]any{"App.menuAutoHide": p.menuHidden} }).
			then(func(p prefs) { h.saidMenuBar(p.menuHidden) }),
		boolPref(prefStatus, false, func(p *prefs) *bool { return &p.statusOn }).
			chooser("App.statusShownIndex", "App.setStatusShownIndex"),
		choicePref(prefExplorer, "left", edges, func(p *prefs) *string { return &p.explorerEdge }).
			chooser("App.explorerEdgeIndex", "App.setExplorerEdge").
			shows(func(p prefs) map[string]any { return map[string]any{"App.explorerEdge": p.explorerEdge} }).
			then(func(prefs) { h.dockPanel("explorer") }), // a new edge is seen at once
		choicePref(prefRelationsEdge, "right", edges, func(p *prefs) *string { return &p.relationsEdge }).
			chooser("App.relationsEdgeIndex", "App.setRelationsEdge").
			shows(func(p prefs) map[string]any { return map[string]any{"App.relationsEdge": p.relationsEdge} }).
			then(func(prefs) { h.dockPanel(relationsPanelSpec().ID) }),
		choicePref(prefTodosEdge, "left", edges, func(p *prefs) *string { return &p.todosEdge }).
			chooser("App.todosEdgeIndex", "App.setTodosEdge").
			shows(func(p prefs) map[string]any { return map[string]any{"App.todosEdge": p.todosEdge} }).
			then(func(prefs) { h.dockPanel(todosPanelSpec().ID) }),
		choicePref(prefKeymap, "vim", keymaps, func(p *prefs) *string { return &p.keymap }).
			chooser("App.keymapIndex", "App.setKeymapIndex").
			shows(func(p prefs) map[string]any {
				return map[string]any{"App.keyset": keysetOf[p.keymap], "App.keymapVim": p.keymap == "vim", "App.keymapText": p.keymap == "text"}
			}).
			then(func(p prefs) { h.say("editor mode: " + keymapLabels[indexOf(keymaps, p.keymap)]) }),
		boolPref(prefWrap, true, func(p *prefs) *bool { return &p.wrap }).
			chooser("App.wrapIndex", "App.setWrapIndex").
			shows(func(p prefs) map[string]any { return map[string]any{"App.editorWrap": p.wrap} }),
		boolPref(prefNumbers, false, func(p *prefs) *bool { return &p.lineNumbers }).
			chooser("App.lineNumbersIndex", "App.setLineNumbersIndex").
			shows(func(p prefs) map[string]any { return map[string]any{"App.lineNumbers": p.lineNumbers} }),
		choicePref(prefIndent, "off", indentMarks, func(p *prefs) *string { return &p.indentMarks }).
			chooser("App.indentMarksIndex", "App.setIndentMarksIndex").
			shows(func(p prefs) map[string]any {
				// View › Indentation marks' radio set too
				return map[string]any{"App.indentMarks": p.indentMarks, "App.indentMarksOff": p.indentMarks == "off",
					"App.indentMarksCursorLine": p.indentMarks == "cursorLine", "App.indentMarksAll": p.indentMarks == "all"}
			}),
		boolPref(prefImages, true, func(p *prefs) *bool { return &p.images }).
			chooser("App.imagesIndex", "App.setImagesIndex").
			shows(func(p prefs) map[string]any { return map[string]any{"App.imagePreviews": p.images} }),
		choicePref(prefOpenAs, "prompt", openAsChoices, func(p *prefs) *string { return &p.openAs }).
			chooser("App.openAsIndex", "App.setOpenAsIndex"),
		boolPref(prefAboutAt, true, func(p *prefs) *bool { return &p.aboutAtStart }).
			chooser("App.aboutAtStartIndex", "App.setAboutAtStartIndex"),
		boolPref(prefUpdates, true, func(p *prefs) *bool { return &p.updateCheck }).
			chooser("App.updateCheckIndex", "App.setUpdateCheckIndex"),
		choicePref(prefCorner, "bottom-right", corners, func(p *prefs) *string { return &p.toastCorner }).
			chooser("App.toastCornerIndex", "App.setToastCorner"),
		toastSeconds,
		// the page: the ruler's columns of text (editable columns: the line numbers' gutter is added
		// to them, syncPageWidth), and its border, whose right edge is the first column past them
		// (vim's colorcolumn at textwidth+1); the editor's guide marks that column too, so a line
		// scrolled past the page's edge still shows where it is
		intPref(prefRuler, defaultRuler, minRuler, maxRuler, func(p *prefs) *int { return &p.ruler }).
			shows(func(p prefs) map[string]any {
				return map[string]any{"App.rulerColumn": p.ruler + 1, "App.rulerText": strconv.Itoa(p.ruler)}
			}),
	}
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
	return prefsForPanels(m, gui, defaultPanelSpecs())
}

func prefsForPanels(m map[string]any, gui bool, specs []PanelSpec) prefs {
	m, _ = migrateAgentDefault(m)
	p := defaultPrefsFor(gui)
	p.panelDefs = specs
	readRows(&p, m, legacyPrefRows(nil, gui))
	str := func(k string) (string, bool) { s, ok := m[k].(string); return s, ok && s != "" }
	if s, ok := str(prefSearchStages); ok {
		p.searchStages = stageChoiceOf(s)
	}
	for k, v := range m {
		readPluginPref(&p, k, v)
	}
	readRows(&p, m, terminalPrefRows(nil))
	readRows(&p, m, agentPrefRows(nil))
	readPanelPrefs(&p, m)
	readPanelFloats(&p, m)
	readFontPrefs(&p, m)
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

// prefState is what the document reads of the composite preferences: the panels', the agents',
// the fonts'. The rows' are rowState's (prefspec.go); the status line's is statusShown's.
func prefState(p prefs) map[string]any {
	m := map[string]any{}
	for _, part := range []map[string]any{panelState(p), floatState(p.panelFloat, p.geometrySpecs()), fontState(p)} {
		maps.Copy(m, part)
	}
	return m
}

// loadPrefs reads the store's preferences on a connection, and applies them: the theme by a
// reload, when it is not the one on screen.
func (h *Host) loadPrefs() {
	tk := h.ticket(nil, TicketSession) // daemon-wide: a workspace switch keeps it, a new connection drops it
	do(h, func(ctx context.Context) answerOf[map[string]any] {
		res, err := h.call(ctx, "preference.list")
		return answered(asMap(res), err)
	}, func(a answerOf[map[string]any]) {
		if !tk.Current() {
			return
		}
		if a.err != nil {
			h.failed("preferences", a.err)
			return
		}
		values, migrated := migrateAgentDefault(a.v)
		p := prefsForPanels(values, h.gui, h.panelSpecs())
		readRows(&p, values, h.prefRows) // the features' own rows too
		h.applyPrefs(p)
		if migrated {
			// The row setter persists even when the projected value already matches.
			core{h}.SetPref(prefAgentDefaultCodename, p.Value(prefAgentDefaultCodename))
		}
	})
}

func (h *Host) applyPrefs(p prefs) {
	if h.reg != nil {
		p.panelDefs = h.panelSpecs()
	}
	h.prefs = p
	st := prefState(p)
	maps.Copy(st, rowState(p, h.prefRows))
	st["App.statusShown"] = h.statusShown()
	h.keep(h.p.SetMany(st))
	h.syncPageWidth()
	h.applyFonts(p)
	h.reg.preferencesChanged() // the SPC p letters the user chose among them (plugins: PreferencesChanged)
	h.applyToastPrefs()
	h.syncStages(true)
	if p.theme != h.theme {
		h.switchTheme(p.theme)
	}
	if !h.started {
		h.started = true // the first preferences of the run: the program has started
		h.reg.started()
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
// readPluginPref reads one tui.plugin.* preference into p: a command's letter or a dialog's
// placement. Anything else is not a plugin's, and is left alone.
func readPluginPref(p *prefs, k string, v any) {
	name, ok := strings.CutPrefix(k, prefPluginPrefix)
	if !ok {
		return
	}
	if plug, id, ok := strings.Cut(name, prefPluginKey); ok {
		if s, isStr := v.(string); isStr {
			if p.pluginKeys == nil {
				p.pluginKeys = map[string]string{}
			}
			p.pluginKeys[plug+"."+id] = s
		}
		return
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

func (h *Host) setPref(name, value string, change func(*prefs)) {
	p := h.prefs
	change(&p)
	h.applyPrefs(p)
	h.storePref(name, value)
}

// storePref writes one preference to the store, behind those waiting, so they land in the order
// set: after setPref's full apply, and after a feature's SetPref through its row (setRowPref).
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
	h.saidMenuBar(v)
}

// saidMenuBar says what the menu bar does now.
func (h *Host) saidMenuBar(hidden bool) {
	if hidden {
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

// setIndentMarks is View › Indentation marks: which lines show their indentation, kept.
func (h *Host) setIndentMarks(name string) {
	if !slices.Contains(indentMarks, name) {
		h.say("no indentation marks " + strconv.Quote(name))
		return
	}
	h.setPref(prefIndent, name, func(p *prefs) { p.indentMarks = name })
}

func (h *Host) toggleStatusLine() {
	v := !h.prefs.statusOn
	h.setPref(prefStatus, strconv.FormatBool(v), func(p *prefs) { p.statusOn = v })
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

// setKeymap switches the editor's keymap, and keeps it.
func (h *Host) setKeymap(name string) {
	if keysetOf[name] == "" {
		h.say("no editor mode " + strconv.Quote(name))
		return
	}
	h.setPref(prefKeymap, name, func(p *prefs) { p.keymap = name })
	h.say("editor mode: " + keymapLabels[indexOf(keymaps, name)])
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

// syncPrefDialog sets the dialog's choosers to the preferences: a chooser moved and not chosen
// shows the value in effect again.
func (h *Host) syncPrefDialog() {
	st := map[string]any{}
	for _, s := range h.prefRows {
		if s.Index != "" {
			st[s.Index] = s.row(h.prefs)
		}
	}
	h.keep(h.p.SetMany(st))
	h.reg.preferencesChanged()
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return 0
}
