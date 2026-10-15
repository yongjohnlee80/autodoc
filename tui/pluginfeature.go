package tui

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/errs"
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
	// the SPC p card's letters: each command's, as bound (plugincmds.go)
	pluginKeyRows    *tuidecl.ListModel
	pluginKeyTargets map[string]string
	running          map[string]*pluginRun
	// the catalog's plugin entries by id, and their letters as F1 names them (plugincmds.go)
	pluginCmds    map[string]pluginCommandRef
	pluginLetters map[string][]string
	// adding and managing them (plugininstall.go): the change PluginConfirm asks about, and the
	// installed ones
	pendingPlugin  *pluginChange
	managedPlugins *rows[managedPlugin]
}

func newPlugins(c pluginHost, opt Plugins) *plugins {
	pl := &plugins{c: c, pluginOpt: opt,
		pluginRows:    tuidecl.NewListModel("key", "kind", "label", "enabled", "target", "rows"),
		pluginKeyRows: tuidecl.NewListModel("key", "target"),
		running:       map[string]*pluginRun{}}
	pl.managedPlugins = newRows(tuidecl.NewListModel("key", "name", "place", "commit", "source"), pl.managedPluginRowOf)
	pl.loadPlugins()
	return pl
}

func (*plugins) Name() string { return "plugins" }

func (pl *plugins) State() map[string]any {
	return map[string]any{
		"App.plugins":            pl.pluginRows,
		"App.pluginKeys":         pl.pluginKeyRows,
		"App.pluginKeysText":     "no plugin commands yet",
		"App.pluginUrl":          "",
		"App.pluginRisk":         pluginRisk,
		"App.pluginConfirmTitle": "",
		"App.pluginQuestion":     "",
		"App.managedPlugins":     pl.managedPlugins.Model(),
		"App.pluginsHelp":        pluginsHelp,
	}
}

func (pl *plugins) Commands() map[string]Command {
	return map[string]Command{
		"App.openPlugin":        oneString("a plugin", pl.openPlugin),
		"App.pluginEntry":       oneString("a plugin's menu row or letter", pl.pluginEntry),
		"App.pluginKey":         oneString("a plugin command letter", pl.pluginKey),
		"App.startAddPlugin":    none(pl.startAddPlugin),
		"App.addPlugin":         oneString("a git URL", pl.addPlugin),
		"App.pluginConfirmed":   none(pl.pluginConfirmed),
		"App.pluginDeclined":    none(pl.pluginDeclined),
		"App.managePlugins":     none(pl.managePlugins),
		"App.startUpdatePlugin": oneNumber("a row", pl.startUpdatePlugin),
		"App.startRemovePlugin": oneNumber("a row", pl.startRemovePlugin),
		"App.placePlugin":       oneNumber("a row", pl.placePlugin),
	}
}

// Catalog is the plugins' user commands (catalog.go); their plugin.* entries, one per [[commands]]
// row, follow the folder through pluginHost (syncPluginEntries).
func (pl *plugins) Catalog() []Entry {
	return []Entry{
		builtin("plugins.add", "add a plugin from a git URL", "App.startAddPlugin()", pl.startAddPlugin),
		builtin("plugins.manage", "manage plugins", "App.managePlugins()", pl.managePlugins),
		builtin("plugins.commands", "plugin commands", "pluginKeys.open()", func() { pl.c.Open("pluginKeys") }),
	}
}

// Keys is SPC p, the plugin commands' card. The card is the plugins' own (views/PluginKeys.qml), its
// letters theirs to bind (plugincmds.go), so it is not a key card of the catalog's: Cards is none.
func (*plugins) Keys() []Key {
	return []Key{{Card: leader, Seq: "p", Show: "p", Does: "plugin commands", Run: "pluginKeys.open()", Group: groupWindow}}
}
func (*plugins) Cards() []Card { return nil }

// PreferencesChanged lays the SPC p letters out again: the user's choices are preferences.
func (pl *plugins) PreferencesChanged() { pl.refreshPlugins() }

// ThemeChanged tells the running plugins the theme the screen now wears.
func (pl *plugins) ThemeChanged() { pl.themePlugins() }

// Stopped stops every running plugin, all at once, each within pluginGrace of being asked to close
// and pluginTermGrace of SIGTERM before SIGKILL (pluginRun.shutdown, whose waits carry their NOTE:):
// quitting waits at most those two for a plugin that hangs, never on it.
func (pl *plugins) Stopped() { pl.stopPlugins() }

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
		p.pluginKeys, p.pluginPlace = maps.Clone(p.pluginKeys), maps.Clone(p.pluginPlace)
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
