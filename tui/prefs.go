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
	prefGUITheme   = "gui.theme"
	prefMenuHide   = "tui.menu.autohide"
	prefStatus     = "tui.status.shown"
	prefTodosEdge  = "tui.todos.edge"
	prefRuler      = "tui.ruler"
	prefKeymap     = "tui.editor.keymap"
	prefRenderer   = "tui.gui.renderer" // the window's renderer, taken as it opens (renderer.go)
	prefWrap       = "tui.editor.wrap"
	prefNumbers    = "tui.editor.linenumbers"
	prefIndent     = "tui.editor.indentmarks"
	prefImages     = "tui.preview.images"
	prefMermaid    = "tui.preview.mermaid"
	prefOpenAs     = "tui.documents.open"
	prefDiskReload = "tui.disk_reload"
	prefLuaGrants  = "lua.grants" // a JSON map: gated script → capability → true (luagate.go)
	prefCorner     = "tui.toast.corner"
	prefAboutAt    = "tui.about.atstart"
	prefUpdates    = "tui.update.check"
	prefSeconds    = "tui.toast.seconds"
	// a plugin's placement: tui.plugin.<name>.placement
	prefPluginPrefix = "tui.plugin."
	prefPluginPlace  = ".placement"
	// a plugin command's letter on its SPC p card: tui.plugin.<name>.key.<id>
	prefPluginKey = ".key."
	// a plugin's letter on the SPC p chooser: tui.plugin.<name>.letter
	prefPluginLetter = ".letter"
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
	theme                string
	language             string // the UI's language (language.go)
	menuHidden, statusOn bool
	explorerEdge         string // where the explorer, with its Links tab, opens
	todosEdge            string // where the todos panel opens: the left edge unless chosen
	ruler                int
	keymap               string
	renderer             string                     // the window's renderer from the next start (renderer.go)
	keymapChosen         bool                       // a keymap was ever stored (tui.editor.keymap): the Vim question is not asked
	wrap, lineNumbers    bool                       // the page: long lines wrapped at its width; each line's number
	indentMarks          string                     // which lines show their indentation as marks (indentMarks)
	images               bool                       // previews as images where the terminal draws them (preview.go)
	mermaid              string                     // what draws a Mermaid diagram in a GUI window: "mermaidjs" or "native" (preview.go)
	openAs               string                     // how a document with two views opens: "prompt", "raw" or "simplified" (files.go)
	diskReload           string                     // a clean file changed on disk: reloaded ("clean") or asked about ("ask") (diskwatch.go)
	luaGrants            map[string]map[string]bool // what each gated Lua script may do (luagate.go)
	aboutAtStart         bool                       // About opens when the program starts (startup.go)
	updateCheck          bool                       // a newer release is looked for when the program starts (startup.go)
	toastCorner          string                     // where the notifications stack
	toastSeconds         int                        // how long a finished one stays
	// the terminal: the edge it opens from; its geometry is in panelGeo
	termEdge string
	// termTheme is the theme the terminals' area is drawn in: "page" (or "") the page's, else one
	// golib ships (terminal.go)
	termTheme string
	// termPanelKeys: Ctrl+h/j/k/l move between panels from a terminal's or agent's input mode too,
	// kept from their programs (terminal.go); on by default
	termPanelKeys bool
	// agentBell: a crew agent's decision request or permission prompt rings (crewbell.go)
	agentBell bool
	// pluginPlace is each plugin's placement the user chose, by name (tui.plugin.<name>.placement);
	// one its manifest does not offer is ignored where it is read (plugins.go placement)
	pluginPlace map[string]string
	// pluginKeys are the letters the user chose on the plugins' SPC p cards, by
	// "<plugin>.<command id>" (tui.plugin.<name>.key.<id>); "" unbinds the command (plugincmds.go)
	pluginKeys map[string]string
	// pluginLetter is each plugin's letter on the SPC p chooser, by name (tui.plugin.<name>.letter,
	// set with the chooser's *); "" is none (pluginpick.go)
	pluginLetter map[string]string
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
	// agentScale is the agent and terminal panels' text, in percent of the window's
	// (tui.agent.text.scale, fonts.go): 0 is the default, defaultAgentScale; the GUI's alone
	agentScale int
	// agentEdge is where the agent panel opens: an edge or the centre (agentplace.go)
	agentEdge string
	// values are the features' own preferences (ValuePref), by name: read with Value
	values map[string]string
	// rows are the declared rows by name, so Value reads any of them; nil before the store is read
	rows map[string]PrefSpec
}

// Value is the preference name as stored: a row's value (a typed row's from its field), or a
// feature's own (ValuePref); "" for a name no row declares. PrefsView's.
func (p prefs) Value(name string) string {
	if s, ok := p.rows[name]; ok && !s.byName && s.value != nil {
		return s.value(p)
	}
	return p.values[name]
}

// PanelGeometry is a panel's size and length at an edge (panelGeometry). PrefsView's.
func (p prefs) PanelGeometry(id, edge string) (size, length int) { return p.panelGeometry(id, edge) }

// SetValue keeps a feature's own preference (ValuePref). PrefsWriter's. A prefs is a value: its copies never
// share a change.
func (p *prefs) SetValue(name, v string) {
	p.values = maps.Clone(p.values)
	if p.values == nil {
		p.values = map[string]string{}
	}
	p.values[name] = v
}

// keepRows lets Value read every one of rows by its name.
func (p *prefs) keepRows(rows ...[]PrefSpec) {
	p.rows = map[string]PrefSpec{}
	for _, rs := range rows {
		for _, s := range rs {
			p.rows[s.Name] = s
		}
	}
}

// asPrefs is the host's preferences a row's closure was given (a prefs, or the *prefs being read).
func asPrefs(v PrefsView) prefs {
	if p, ok := v.(*prefs); ok {
		return *p
	}
	return v.(prefs)
}

func defaultPrefs() prefs { return defaultPrefsFor(false) }

// defaultPrefsFor are the defaults of the terminal's UI, or of the GUI's: they differ in the
// theme alone.
func defaultPrefsFor(gui bool) prefs {
	p := prefs{searchStages: allStagesChecked()}
	readRows(&p, nil, legacyPrefRows(nil, gui))
	readRows(&p, nil, terminalPrefRows(nil))
	readRows(&p, nil, agentPrefRows(nil))
	p.keepRows(legacyPrefRows(nil, gui), terminalPrefRows(nil), agentPrefRows(nil))
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
	names := themeNames
	if h != nil {
		names = h.themeNames()
	}
	themeRow := choicePref(themePref(gui), theme, names, func(p *prefs) *string { return &p.theme }).
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
		choicePref(prefLanguage, "en", languages, func(p *prefs) *string { return &p.language }).
			chooser("App.languageIndex", "App.setLanguageIndex").
			shows(func(v PrefsView) map[string]any { p := asPrefs(v); return languageChecks(p.language) }),
		// the menu bar and the status line show for a new user (v0.3.3); a stored choice wins
		boolPref(prefMenuHide, false, func(p *prefs) *bool { return &p.menuHidden }).
			chooser("App.menuHiddenIndex", "App.setMenuHiddenIndex").
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				return map[string]any{"App.menuAutoHide": h.menuAutoHide(p)}
			}).
			then(func(v PrefsView) { p := asPrefs(v); h.saidMenuBar(p.menuHidden) }),
		boolPref(prefStatus, true, func(p *prefs) *bool { return &p.statusOn }).
			chooser("App.statusShownIndex", "App.setStatusShownIndex"),
		choicePref(prefExplorer, "left", edges, func(p *prefs) *string { return &p.explorerEdge }).
			chooser("App.explorerEdgeIndex", "App.setExplorerEdge").
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				return map[string]any{"App.explorerEdge": p.explorerEdge}
			}).
			then(func(PrefsView) { h.dockPanel("explorer") }), // a new edge is seen at once
		choicePref(prefTodosEdge, "left", edges, func(p *prefs) *string { return &p.todosEdge }).
			chooser("App.todosEdgeIndex", "App.setTodosEdge").
			shows(func(v PrefsView) map[string]any { p := asPrefs(v); return map[string]any{"App.todosEdge": p.todosEdge} }).
			then(func(PrefsView) { h.dockPanel(todosPanelSpec().ID) }),
		choicePref(prefKeymap, "vim", keymaps, func(p *prefs) *string { return &p.keymap }).
			chooser("App.keymapIndex", "App.setKeymapIndex").
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				return map[string]any{"App.keyset": keysetOf[p.keymap], "App.keymapVim": p.keymap == "vim", "App.keymapText": p.keymap == "text"}
			}).
			then(func(v PrefsView) {
				p := asPrefs(v)
				h.say(h.trf("autodoc.notice.prefs.editor_mode", keymapLabels[indexOf(keymaps, p.keymap)]))
			}),
		choicePref(prefRenderer, RendererAuto, renderers, func(p *prefs) *string { return &p.renderer }).
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				h.mirrorRenderer(p.renderer) // the next start's window reads it before the store is read
				return map[string]any{"App.rendererAuto": p.renderer == RendererAuto, "App.rendererOpenGL": p.renderer == RendererOpenGL}
			}),
		boolPref(prefWrap, true, func(p *prefs) *bool { return &p.wrap }).
			chooser("App.wrapIndex", "App.setWrapIndex").
			shows(func(v PrefsView) map[string]any { p := asPrefs(v); return map[string]any{"App.editorWrap": p.wrap} }),
		boolPref(prefNumbers, false, func(p *prefs) *bool { return &p.lineNumbers }).
			chooser("App.lineNumbersIndex", "App.setLineNumbersIndex").
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				return map[string]any{"App.lineNumbers": p.lineNumbers}
			}),
		choicePref(prefIndent, "off", indentMarks, func(p *prefs) *string { return &p.indentMarks }).
			chooser("App.indentMarksIndex", "App.setIndentMarksIndex").
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				// View › Indentation marks' radio set too
				return map[string]any{"App.indentMarks": p.indentMarks, "App.indentMarksOff": p.indentMarks == "off",
					"App.indentMarksCursorLine": p.indentMarks == "cursorLine", "App.indentMarksAll": p.indentMarks == "all"}
			}),
		boolPref(prefImages, true, func(p *prefs) *bool { return &p.images }).
			chooser("App.imagesIndex", "App.setImagesIndex").
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				return map[string]any{"App.imagePreviews": p.images}
			}),
		choicePref(prefMermaid, "mermaidjs", mermaidEngines, func(p *prefs) *string { return &p.mermaid }).
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				// File › Preview option's radio set
				return map[string]any{"App.mermaidNative": p.mermaid == "native", "App.mermaidJS": p.mermaid != "native"}
			}),
		choicePref(prefOpenAs, "prompt", openAsChoices, func(p *prefs) *string { return &p.openAs }).
			chooser("App.openAsIndex", "App.setOpenAsIndex"),
		choicePref(prefDiskReload, diskReloadClean, diskReloadChoices, func(p *prefs) *string { return &p.diskReload }).
			chooser("App.diskReloadIndex", "App.setDiskReloadIndex"),
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
			shows(func(v PrefsView) map[string]any {
				p := asPrefs(v)
				return map[string]any{"App.rulerColumn": p.ruler + 1, "App.rulerText": strconv.Itoa(p.ruler)}
			}),
	}
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
	m = migrateLinksPanel(m)
	p := defaultPrefsFor(gui)
	p.panelDefs = specs
	readRows(&p, m, legacyPrefRows(nil, gui))
	str := func(k string) (string, bool) { s, ok := m[k].(string); return s, ok && s != "" }
	if s, ok := str(prefSearchStages); ok {
		p.searchStages = stageChoiceOf(s)
	}
	if s, ok := str(prefLuaGrants); ok {
		p.luaGrants = luaGrantsOf(s)
	}
	_, p.keymapChosen = str(prefKeymap) // the first run's Vim question asks only while none is (vimquestion.go)
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
// reload, when it is not the one on screen. What is set while the list is read (a panel resized as
// the TUI connects) is laid over its answer: the list may have been read before that write landed,
// and must not undo it.
func (h *Host) loadPrefs() {
	tk := h.ticket(nil, TicketSession) // daemon-wide: a workspace switch keeps it, a new connection drops it
	if h.prefsLoading == 0 {
		h.prefsSetMeanwhile = map[string]string{}
	}
	h.prefsLoading++
	do(h, func(ctx context.Context) answerOf[map[string]any] {
		res, err := h.call(ctx, "preference.list")
		return answered(asMap(res), err)
	}, func(a answerOf[map[string]any]) {
		meanwhile := h.prefsSetMeanwhile
		if h.prefsLoading--; h.prefsLoading == 0 {
			h.prefsSetMeanwhile = nil
		}
		if !tk.Current() {
			return
		}
		if a.err != nil {
			// the store's are not to be had: the run starts all the same, on the defaults and what
			// was set meanwhile (h.prefs), the error said, rather than stay half started with no word
			// why; a later list that answers applies the store's as a change, starting nothing again
			h.failed("preferences", a.err)
			h.startOnce()
			return
		}
		if a.v == nil {
			a.v = map[string]any{}
		}
		for name, value := range meanwhile {
			a.v[name] = value
		}
		values, migrated := migrateAgentDefault(a.v)
		values = migrateLinksPanel(values) // readRows reads the explorer's edge from it too
		p := prefsForPanels(values, h.gui, h.panelSpecs())
		readRows(&p, values, h.prefRows) // the features' own rows too
		p.keepRows(h.prefRows)
		h.applyPrefs(p)
		h.startOnce()
		h.luaPrefsRead = true
		h.askLuaGrants()   // a gated script's question waits for the answers already given
		h.decideLaunches() // so does a plugin's start with AutoDoc (pluginlaunch.go)
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
	h.record(h.p.SetMany(st))
	h.syncPageWidth()
	h.applyFonts(p)
	h.applyTerminalKeymap(p)   // the terminals' normal mode for the keymap (terminalkeymap.go)
	h.reg.preferencesChanged() // the SPC p letters the user chose among them (plugins: PreferencesChanged)
	h.applyToastPrefs()
	h.syncStages(true)
	if p.theme != h.theme {
		h.switchTheme(p.theme)
	}
	h.useLanguage(p.language) // at the start, and after a choice: nothing when it is the one shown
}

// startOnce is the program's start, once a run: the store's preferences are in (loadPrefs's
// answer), so what starts reads them, never the defaults; when the store can't answer, on the
// defaults, the error said. A preference set before they answer
// (setPref, a theme chosen in the first moments) applies at once, but starts nothing: started
// from it, About opened on the defaults though the store said not to, and the update check and
// every Starter read defaults too.
func (h *Host) startOnce() {
	if h.started {
		return
	}
	h.started = true
	h.reg.started()
	h.refreshDock() // the editor, and whatever opened before the start
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
		h.set("App.searchTitle", h.tr("autodoc.text.prefs.search"))
	}
}

// setPref changes one preference: on screen at once, and in the store, in the order the changes
// were made. Each write is its own call on its own worker, and the daemon may answer them in any
// order: sent together, an earlier change could land after a later one and the store keep it, so
// the next start shows what the user changed away from. They go one at a time instead.
// readPluginPref reads one tui.plugin.* preference into p: a command's letter, a plugin's letter or
// a dialog's placement. Anything else is not a plugin's, and is left alone.
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
	if plug, ok := strings.CutSuffix(name, prefPluginLetter); ok {
		if s, isStr := v.(string); isStr {
			if p.pluginLetter == nil {
				p.pluginLetter = map[string]string{}
			}
			p.pluginLetter[plug] = s
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
	if h.prefsSetMeanwhile != nil {
		h.prefsSetMeanwhile[name] = value // a list in flight may not hold it yet (loadPrefs)
	}
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
			h.failed(h.tr("autodoc.notice.prefs.save_the_preference"), err)
		}
		h.prefWrites = h.prefWrites[1:]
		if len(h.prefWrites) > 0 {
			h.writePref()
		}
	})
}

// The preference commands, as the menu, the leader and the Preferences dialog give them.

// toggleTerminalPanelKeys is Options › Terminal's item: the other setting, applied at once, so the
// terminals' host keys follow it (applyTerminalKeymap). Through the row alone (Core.SetPref) the
// keys would keep the old setting until the next apply.
func (h *Host) toggleTerminalPanelKeys() {
	v := !h.prefs.termPanelKeys
	h.setPref(prefTermPanelKeys, strconv.FormatBool(v), func(p *prefs) { p.termPanelKeys = v })
}

func (h *Host) toggleMenuBar() {
	v := !h.prefs.menuHidden
	h.setPref(prefMenuHide, strconv.FormatBool(v), func(p *prefs) { p.menuHidden = v })
	h.saidMenuBar(v)
}

// saidMenuBar says what the menu bar does now.
func (h *Host) saidMenuBar(hidden bool) {
	if hidden {
		h.say(h.tr("autodoc.notice.prefs.the_menu_bar_hides_f10_or_alt_letter_brings_it_u"))
	} else {
		h.say(h.tr("autodoc.notice.prefs.the_menu_bar_shows"))
	}
}

// leaderMenuBar is SPC m: the menu bar shown and focused, or hidden. The bar shown is focused at
// once: it is laid out after this key, and golib's FocusInto waits for that layout, which runs before
// the next key is routed, so a key typed right after SPC m (Enter, say) reaches the bar. A focus
// posted for later lost that race to the keys already typed.
func (h *Host) leaderMenuBar() {
	h.toggleMenuBar()
	if !h.prefs.menuHidden {
		h.keep(h.p.Call("menuBar", "forceActiveFocus"))
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
		h.say(h.trf("autodoc.notice.prefs.no_indentation_marks", strconv.Quote(name)))
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
		h.set("App.prefsError", h.trf("autodoc.text.prefs.the_page_s_width_is_a_number_of_columns_to", strconv.Itoa(minRuler), strconv.Itoa(maxRuler)))
		return
	}
	h.set("App.prefsError", "")
	h.setPref(prefRuler, strconv.Itoa(n), func(p *prefs) { p.ruler = n })
}

// useTheme is View › Theme and the Preferences dialog: the theme on screen, and kept.
func (h *Host) useTheme(name string) {
	if _, err := h.themeValues(name); err != nil {
		h.notify(err.Error())
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
		h.say(h.trf("autodoc.notice.prefs.no_editor_mode", strconv.Quote(name)))
		return
	}
	h.setPref(prefKeymap, name, func(p *prefs) { p.keymap, p.keymapChosen = name, true })
	h.say(h.trf("autodoc.notice.prefs.editor_mode", keymapLabels[indexOf(keymaps, name)]))
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

// prefChoosers is the feature the preferences' choosers belong to: every row's index source and
// set command, the host's own rows and the features'.
type prefChoosers struct{ h *Host }

func (prefChoosers) Name() string { return "preferences" }

func (c prefChoosers) State() map[string]any { return rowState(c.h.prefs, c.h.prefRows) }

func (c prefChoosers) Commands() map[string]Command {
	m := map[string]Command{}
	for _, s := range c.h.prefRows {
		if s.Set != "" {
			m[s.Set] = oneNumber("a row", func(i int) { c.h.choosePref(s, i) })
		}
	}
	return m
}

// choosePref is a chooser's row i chosen: the row's value, on screen and kept, then what follows
// a change of it. The value in effect is kept too, when chosen: it may be a default the store does
// not hold, and the user who chose it keeps it when the default changes. A row out of range
// changes nothing.
func (h *Host) choosePref(s PrefSpec, i int) {
	if i < 0 || i >= len(s.Choices) {
		return
	}
	v := s.Choices[i]
	changed := v != s.value(h.prefs)
	h.setPref(s.Name, v, func(p *prefs) { s.read(p, v) })
	if changed && s.Changed != nil {
		s.Changed(h.prefs)
	}
}

// setRowPref sets row's preference to v through the row alone (Core.SetPref): read into the live
// prefs, its chooser's row and its State published, its Changed run when v changed, then stored in
// order. A row
// that a feature's code sets (the recent files on every open) does not re-apply every preference,
// as a choice in the dialog does (choosePref).
func (h *Host) setRowPref(row PrefSpec, v string) {
	changed := v != row.value(h.prefs)
	p := h.prefs
	row.read(&p, v)
	h.prefs = p
	if st := rowState(p, []PrefSpec{row}); len(st) > 0 {
		h.keep(h.p.SetMany(st)) // the chooser's row (Index) and the row's State
	}
	if changed && row.Changed != nil {
		row.Changed(p)
	}
	if changed {
		h.reg.prefSet(row.Name)
	}
	h.storePref(row.Name, v)
}

// choosePrefNamed is choosePref by the preference's name: the menu's and the tests' way to a row.
func (h *Host) choosePrefNamed(name string, i int) {
	for _, s := range h.prefRows {
		if s.Name == name {
			h.choosePref(s, i)
			return
		}
	}
}

// boolPref is a yes-or-no preference kept as "true" or "false", yes first in its chooser.
func boolPref(name string, def bool, field func(*prefs) *bool) PrefSpec {
	return BoundPref(name, []string{"true", "false"}, PrefBinding{
		Read:  func(p PrefsWriter, v string) { *field(p.(*prefs)) = v == "true" },
		Value: func(v PrefsView) string { p := asPrefs(v); return strconv.FormatBool(*field(&p)) },
		Reset: func(p PrefsWriter) { *field(p.(*prefs)) = def }})
}

// choicePref is one of a list of names.
func choicePref(name, def string, choices []string, field func(*prefs) *string) PrefSpec {
	return BoundPref(name, choices, PrefBinding{
		Read:  func(p PrefsWriter, v string) { *field(p.(*prefs)) = v },
		Value: func(v PrefsView) string { p := asPrefs(v); return *field(&p) },
		Reset: func(p PrefsWriter) { *field(p.(*prefs)) = def }})
}

// intPref is a whole number from lo to hi. Its chooser, when it has one, offers each of them.
func intPref(name string, def, lo, hi int, field func(*prefs) *int) PrefSpec {
	return BoundPref(name, nil, PrefBinding{
		Valid: func(v string) bool { n, err := strconv.Atoi(v); return err == nil && n >= lo && n <= hi },
		Read:  func(p PrefsWriter, v string) { *field(p.(*prefs)), _ = strconv.Atoi(v) },
		Value: func(v PrefsView) string { p := asPrefs(v); return strconv.Itoa(*field(&p)) },
		Reset: func(p PrefsWriter) { *field(p.(*prefs)) = def }})
}
