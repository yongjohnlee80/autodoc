package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/errs"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE PLUGINS, A FEATURE — the Plugins menu, its dialogs and services, their commands on SPC p and
// in the catalog, the document feed, and adding and managing plugins (plugins.go and the plugin*.go
// files). The feature reaches the host through pluginHost: Core, and the few services only a plugin
// needs, each scoped to what is the plugins' own (their plugin.* catalog entries, their tui.plugin.*
// preferences).

// plugins is the plugins: the folder's, as found; those running; and their menu, letters and
// commands.
type plugins struct {
	c         pluginHost
	pluginOpt Plugins
	// the folder's plugins, as found, and the Plugins menu's rows
	pluginList []pluginEntry
	pluginRows *tuidecl.ListModel
	feedGen    int // numbers the document feed's pending send (pluginfeed.go)
	// SPC p: the chooser's plugins, its cursor, the plugin * asks a letter for and what it last said
	// (pluginpick.go); then each plugin's card, and the one shown in the list's place ("" for the
	// list; plugincmds.go)
	chooser     []pluginRow
	chooserAt   int
	chooserBind string
	chooserNote string
	pluginCards map[string]*pluginCard
	cardPlugin  string
	running     map[string]*pluginRun
	// the catalog's plugin entries by id, and their letters as F1 names them (plugincmds.go)
	pluginCmds    map[string]pluginCommandRef
	pluginLetters map[string][]string
	// adding and managing them (plugininstall.go): the change PluginConfirm asks about, and the
	// installed ones
	pendingPlugin  *pluginChange
	managedPlugins *rows[managedPlugin]
	// the results panel's table, a plugin's (plugintable.go), and its export while one runs, in
	// the format last chosen (plugintable_export.go)
	results      *resultsTable
	export       *resultsExport
	exportFormat copyFormat
	// the bar (plugintable.go): its clause and each clause's text; ORDER BY's applied text and why
	// it did not parse, WHERE's why, the fields it offers for the word typed, and the view's note
	resultsClause                                                                           int
	clauseText                                                                              [2]string
	resultsOrder, resultsOrderErr, resultsFilter, resultsWhereErr, resultsHint, resultsNote string
	whereOn                                                                                 bool // a WHERE filters the view
	// the card plugins' panels, by name and in order, fixed when the program is built
	// (pluginpanels.go); nil until then
	cards     map[string]*cardPanel
	cardOrder []*cardPanel
}

func newPlugins(c pluginHost, opt Plugins) *plugins {
	pl := &plugins{c: c, pluginOpt: opt,
		pluginRows: tuidecl.NewListModel("key", "kind", "label", "enabled", "target", "rows"),
		running:    map[string]*pluginRun{}}
	pl.managedPlugins = newRows(tuidecl.NewListModel("key", "name", "place", "commit", "source"), pl.managedPluginRowOf)
	pl.results = &resultsTable{}
	pl.results.model = newResultsModel(pl.results, pl.fetchRows)
	pl.results.shown = pl.results.model
	pl.exportFormat = formatCSV
	pl.loadPlugins()
	return pl
}

func (*plugins) Name() string { return "plugins" }

func (pl *plugins) State() map[string]any {
	size, length := pl.c.Prefs().panelGeometry(resultsPanel, "bottom")
	st := map[string]any{
		"App.plugins":            pl.pluginRows,
		"App.pluginChooserTitle": "SPC p — plugins",
		"App.pluginChooserText":  "",
		"App.pluginChooserHelp":  chooserHelp,
		"App.pluginUrl":          "",
		"App.pluginRisk":         pluginRisk,
		"App.pluginConfirmTitle": "",
		"App.pluginQuestion":     "",
		"App.managedPlugins":     pl.managedPlugins.Model(),
		"App.pluginsHelp":        pluginsHelp,
		"App.resultsRows":        pl.results.model,
		"App.resultsTitle":       "results",
		"App.resultsClauses":     resultsClausesModel(),
		"App.resultsClauseIndex": 0,
		"App.resultsClauseText":  "",
		"App.resultsOrder":       "",
		"App.resultsViewNote":    "",
		"App.resultsFilter":      "",
		"App.exportCanSave":      true,
		"App.resultsSortLine":    "",
		"App.exportHeading":      "",
		"App.exportMore":         "",
		"App.exportMoreShown":    false,
		"App.exportFormats":      exportFormatsModel(),
		"App.exportFormatIndex":  0,
		"App.exportFile":         "",
		"App.resultsShown":       false,
		"App.resultsSize":        size,
		"App.resultsLength":      length,
	}
	maps.Copy(st, pl.cardState())
	return st
}

func (pl *plugins) Commands() map[string]Command {
	return map[string]Command{
		"App.openPlugin":          oneString("a plugin", pl.openPlugin),
		"App.pluginEntry":         oneString("a plugin's menu row or letter", pl.pluginEntry),
		"App.openPluginChooser":   none(pl.openPluginChooser),
		"App.pluginChooserKey":    oneString("a key on the SPC p chooser", pl.pluginChooserKey),
		"App.startAddPlugin":      none(pl.startAddPlugin),
		"App.addPlugin":           oneString("a git URL", pl.addPlugin),
		"App.pluginConfirmed":     none(pl.pluginConfirmed),
		"App.pluginDeclined":      none(pl.pluginDeclined),
		"App.managePlugins":       none(pl.managePlugins),
		"App.startUpdatePlugin":   oneNumber("a row", pl.startUpdatePlugin),
		"App.startRemovePlugin":   oneNumber("a row", pl.startRemovePlugin),
		"App.placePlugin":         oneNumber("a row", pl.placePlugin),
		"App.toggleResults":       none(func() { pl.c.TogglePanel(resultsPanel) }),
		"App.copyResults":         none(func() { pl.copyResults(formatTSV) }),
		"App.startExportResults":  none(func() { pl.startExportResults("") }),
		"App.exportFormatChosen":  oneNumber("a format's row", pl.exportFormatChosen),
		"App.chooseExportFile":    none(pl.chooseExportFile),
		"App.exportResults":       oneString("a file's path", pl.exportResults),
		"App.insertResults":       none(pl.insertResults),
		"App.resultsClauseChosen": numberAndString("a clause and the text typed", pl.resultsClauseChosen),
		"App.applyResultsClause":  oneString("a clause's text", pl.applyResultsClause),
		"App.resultsClauseTyped":  oneString("a clause's text", pl.resultsClauseTyped),
		"App.resultsOrderChanged": oneString("an ORDER BY", pl.resultsOrderChanged),
	}
}

// Catalog is the plugins' user commands (catalog.go); their plugin.* entries, one per [[commands]]
// row, follow the folder through pluginHost (syncPluginEntries).
func (pl *plugins) Catalog() []Entry {
	return []Entry{
		builtin("plugins.add", "add a plugin from a git URL", "App.startAddPlugin()", pl.startAddPlugin),
		builtin("plugins.manage", "manage plugins", "App.managePlugins()", pl.managePlugins),
		builtin("plugins.commands", "plugin commands: choose a plugin, then one of its commands", "App.openPluginChooser()", pl.openPluginChooser),
		// the results panel (plugintable.go), and its rows, each command saying which rows it takes
		builtin("results.toggle", "results panel", "App.toggleResults()", func() { pl.c.TogglePanel(resultsPanel) }),
		builtin("results.copy", "results: copy selection (the selected cells, or the cursor's, as TSV)", "App.copyResults()", func() { pl.copyResults(formatTSV) }),
		builtin("results.copy_csv", "results: copy selection as CSV (its fields as a header)", "", func() { pl.copyResults(formatCSV) }),
		builtin("results.copy_json", "results: copy selection as JSON (an object per row)", "", func() { pl.copyResults(formatJSON) }),
		builtin("results.copy_markdown", "results: copy selection as a Markdown table", "", func() { pl.copyResults(formatMarkdown) }),
		builtin("results.select_all", "results: select every loaded row", "", pl.selectAllResults),
		{ID: "results.sort_asc", Title: "results: sort the loaded rows by a field, ascending", Args: "[field]",
			Help: "results: sort the loaded rows by a field (its 1-based position; the cursor's when none), ascending",
			Run:  func(inv Invocation) error { return pl.sortResults("asc", inv.Args) }},
		{ID: "results.sort_desc", Title: "results: sort the loaded rows by a field, descending", Args: "[field]",
			Help: "results: sort the loaded rows by a field (its 1-based position; the cursor's when none), descending",
			Run:  func(inv Invocation) error { return pl.sortResults("desc", inv.Args) }},
		{ID: "results.unsort", Title: "results: unsort, the rows in the query's order", Help: "results: unsort, the rows in the query's order",
			Run: func(inv Invocation) error { return pl.sortResults("off", inv.Args) }},
		builtin("results.export", "results: export loaded rows…", "App.startExportResults()", func() { pl.startExportResults("") }),
		builtin("results.csv", "results: export loaded rows as CSV…", "", func() { pl.startExportResults(formatCSV) }),
		builtin("results.markdown", "results: insert loaded rows as a Markdown table", "App.insertResults()", pl.insertResults),
	}
}

// Panels is the results panel: a plugin's table (plugintable.go), at the bottom unless moved. Its
// closing lets the table go, and the keyboard goes back to the page. Then a panel for each card
// plugin (pluginpanels.go).
func (pl *plugins) Panels() []PanelSpec {
	s := resultsPanelSpec()
	s.Opened = func(c PanelHost) {
		c.Set("App.resultsShown", true)
		pl.bindResultsMenu()
	}
	s.Closed = func(c PanelHost) {
		c.Set("App.resultsShown", false)
		pl.resultsClosed()
		c.Keep(c.RestoreKeyboardPane("editor"))
	}
	return append([]PanelSpec{s}, pl.cardSpecs()...)
}

// resultsPanelSpec is the results panel: at the bottom, 35% of the window tall and its full width
// until the user sizes it, its size kept as every panel's is (panelsize.go).
func resultsPanelSpec() PanelSpec {
	return PanelSpec{ID: resultsPanel, View: resultsView, Priority: 70,
		Edge: func(prefs) string { return "bottom" }, Places: []string{"bottom"},
		Size: [2]int{10, 90}, Length: [2]int{20, 100}, Default: func(string) (int, int) { return 35, 100 }}
}

// Keys is SPC p, the plugins' chooser, then a plugin's card in its place. It is the plugins' own
// (views/PluginChooser.qml), its letters the user's and the plugins' (pluginpick.go, plugincmds.go),
// so it is not a key card of the catalog's: Cards is none.
func (*plugins) Keys() []Key {
	return []Key{{Card: leader, Seq: "p", Show: "p", Does: "plugin commands", Run: "App.openPluginChooser()", Group: groupWindow}}
}
func (*plugins) Cards() []Card { return nil }

// PreferencesChanged lays the SPC p letters out again, the plugins' and their commands': the user's
// choices are preferences; and the
// results panel takes the size the user left it at.
func (pl *plugins) PreferencesChanged() {
	pl.refreshPlugins()
	size, length := pl.c.Prefs().panelGeometry(resultsPanel, "bottom")
	pl.c.Set("App.resultsSize", size)
	pl.c.Set("App.resultsLength", length)
	pl.syncCards()
}

// ThemeChanged tells the running plugins the theme the screen now wears.
func (pl *plugins) ThemeChanged() { pl.themePlugins() }

// Stopped stops every running plugin, all at once, each within pluginGrace of being asked to close
// and pluginTermGrace of SIGTERM before SIGKILL (pluginRun.shutdown, whose waits carry their NOTE:):
// quitting waits at most those two for a plugin that hangs, never on it.
// An export not finished is cancelled first, its .part removed.
func (pl *plugins) Stopped() {
	pl.stopExport()
	pl.stopPlugins()
}

// The page's lifecycle is the document feed's: a page shown, a pause in typing and a cursor move
// each send the document once things rest (feedSoon). Its Version is the page's own (Page.Version),
// so a shown page and an edit raise it and a move does not.
func (pl *plugins) Shown(Page)  { pl.feedSoon() }
func (pl *plugins) Edited(Page) { pl.feedSoonAfter(max(pluginFeedDelay-editPause, 0)) }
func (pl *plugins) Moved(int, int) {
	pl.feedSoon()
}
func (*plugins) Reread(Page) {}
func (*plugins) Closed()     {}

// pluginHost is what the plugins reach of the host: Core, and the services only a plugin needs. The
// catalog and preference services are scoped: a plugin.* entry, a tui.plugin.* preference; anything
// else is refused with ErrNotPluginEntry or ErrNotPluginPref.
type pluginHost interface {
	Core
	// Attached reports whether the program runs: a plugin's dialog and the card's text need it
	Attached() bool
	// Overlay is where a dialog plugin's float attaches
	Overlay() (*widget.OverlayHost, bool)
	// HasCatalog reports whether the catalog is built (the registry is assembled)
	HasCatalog() bool
	PluginEntries() []*Entry                 // the catalog's plugin.* entries
	AddPluginEntry(e Entry) error            // a plugin.* entry; ErrNotPluginEntry for any other
	RemovePluginEntries()                    // every plugin.* entry
	CommandsChanged()                        // F1 and the command line list the commands again
	RunEntry(id string, args []string) error // a plugin's host.call of a catalog command
	// SetPluginPref sets a tui.plugin.* preference, applied and stored; ErrNotPluginPref for any other
	SetPluginPref(name, value string) error
	Context() context.Context // the program's lifetime: a running plugin's calls end with it
	// After runs fn on the loop once d has passed (the feed's rest after typing and moving)
	After(d time.Duration, fn func())
	Theme() string                                        // the theme the screen wears
	Cursor() (line, col int)                              // the editor's cursor, in its columns
	Selection() (line, col, endLine, endCol int, ok bool) // the editor's selection, if any
	NotifyOngoing(id, msg string)
	NotifyDone(id, msg string)
	Keep(err error)
	// PanelOpen reports whether panel id is open (the results panel opens for a plugin's table)
	PanelOpen(id string) bool
	// DockPanel forgets a panel's floating rectangle: a card's, moved to another edge
	DockPanel(id string)
	// PageSize is the page's size in cells, as last laid out; 0 before its first layout. A card's
	// panel is sized from it until the user sizes it
	PageSize() (w, h int)
	// TableCore is the table behind a cell-cursor TableView, by its id; nil when there is none
	TableCore(id string) *widget.TableCore
	// ResultsTable is the cell-cursor TableView's DataTable, by its id: its menu; nil when none
	ResultsTable(id string) *widget.DataTable
	// CopyText puts text on the clipboard, reporting whether there is one
	CopyText(text string) bool
	// SetEditorRegister makes a table yank available to the editor's p/P commands.
	SetEditorRegister(text string)
	// InsertBlock puts text into the page at the cursor, on lines of its own: one undo step
	InsertBlock(text string) error
	// BrowseFolder is where a file dialog starts: the open file's folder, the workspace's, or home
	BrowseFolder() string
}

// The scoped services' refusals.
var (
	ErrNotPluginEntry = errs.Sentinel(errs.ErrInvalidArgument, "not a plugin.* catalog entry")
	ErrNotPluginPref  = errs.Sentinel(errs.ErrInvalidArgument, "not a tui.plugin.* preference")
)

// The host's pluginHost: core's, with these.

func (c core) Attached() bool                          { return c.h.p != nil }
func (c core) Overlay() (*widget.OverlayHost, bool)    { return c.h.p.Overlay() }
func (c core) HasCatalog() bool                        { return c.h.cat != nil }
func (c core) CommandsChanged()                        { c.h.commandsChanged() }
func (c core) RunEntry(id string, args []string) error { return c.h.cat.run(id, args) }
func (c core) Context() context.Context                { return c.h.ctx }
func (c core) After(d time.Duration, fn func())        { c.h.after(d, fn) }
func (c core) Theme() string                           { return c.h.theme }
func (c core) NotifyOngoing(id, msg string)            { c.h.notifyOngoing(id, msg) }
func (c core) NotifyDone(id, msg string)               { c.h.notifyDone(id, msg) }
func (c core) BrowseFolder() string                    { return c.h.browseFolder() }

func (c core) PageSize() (w, h int) {
	ed, ok := c.h.editor.(interface{ Context() *tuicore.Context })
	if !ok || ed.Context() == nil {
		return 0, 0
	}
	ctx := ed.Context()
	r, ok := ctx.ResolveAnchor(ctx.NodeAnchor())
	if !ok {
		return 0, 0
	}
	return r.W, r.H
}

func (c core) TableCore(id string) *widget.TableCore {
	if t := c.ResultsTable(id); t != nil {
		return t.Core()
	}
	return nil
}

func (c core) ResultsTable(id string) *widget.DataTable {
	if c.h.p == nil {
		return nil
	}
	comp, ok := c.h.p.Find(id)
	if !ok {
		return nil
	}
	return tuidecl.TableOf(comp)
}

func (c core) CopyText(text string) bool { return c.h.p != nil && c.h.p.App().CopyToClipboard(text) }

func (c core) SetEditorRegister(text string) {
	if c.h.core != nil {
		c.h.core.SetRegister(text, false)
	}
}

// InsertBlock puts text into the page as a block of its own: on the cursor's line when that is
// empty, else after it, with a blank line before and after wherever its neighbour has text, so a
// Markdown block (a table) neither joins the paragraph above nor swallows the line below. One undo
// step; a read-only page refuses it.
func (c core) InsertBlock(text string) error {
	ed := c.h.core
	if ed == nil {
		return errors.New("no page is open")
	}
	if ed.ReadOnly() {
		return widget.ErrReadOnly
	}
	blank := func(ln int) bool { return strings.TrimSpace(ed.LineAt(ln)) == "" }
	line, _ := ed.Line()
	text = strings.TrimSuffix(text, "\n")
	at := widget.TextPos{Line: line}
	if blank(line) {
		if line > 0 && !blank(line-1) {
			text = "\n" + text
		}
	} else {
		at.Col = len(ed.LineAt(line)) // past its last cluster: clamped to the line's end
		text = "\n\n" + text
	}
	if line+1 < ed.LineCount() && !blank(line+1) {
		text += "\n"
	}
	return ed.Batch(func() error {
		_, err := ed.Insert(at, text)
		return err
	})
}

func (c core) ThemeValues(name string) (map[string]string, error) { return c.h.themeValues(name) }

func (c core) PluginEntries() []*Entry {
	if c.h.cat == nil {
		return nil
	}
	var out []*Entry
	for _, e := range c.h.cat.entries() {
		if e.Source == SourcePlugin {
			out = append(out, e)
		}
	}
	return out
}

func (c core) AddPluginEntry(e Entry) error {
	if e.Source != SourcePlugin || !strings.HasPrefix(e.ID, "plugin.") {
		return fmt.Errorf("%s: %w", e.ID, ErrNotPluginEntry)
	}
	return c.h.cat.add(e)
}

func (c core) RemovePluginEntries() {
	for _, e := range c.PluginEntries() {
		c.h.cat.remove(e.ID)
	}
}

func (c core) SetPluginPref(name, value string) error {
	if !strings.HasPrefix(name, prefPluginPrefix) {
		return fmt.Errorf("%s: %w", name, ErrNotPluginPref)
	}
	c.h.setPref(name, value, func(p *prefs) {
		// the maps are shared with the prefs in effect: written anew, never in place
		p.pluginKeys, p.pluginPlace, p.pluginLetter = maps.Clone(p.pluginKeys), maps.Clone(p.pluginPlace), maps.Clone(p.pluginLetter)
		readPluginPref(p, name, value)
	})
	return nil
}

func (c core) Cursor() (line, col int) {
	if c.h.core == nil {
		return 0, 0
	}
	return c.h.core.Line()
}

func (c core) Selection() (line, col, endLine, endCol int, ok bool) {
	if c.h.core == nil {
		return 0, 0, 0, 0, false
	}
	return c.h.core.SelectionRange()
}
