package tui

import (
	"slices"

	"github.com/yongjohnlee80/golib/tui/decl/themes"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// PLUGINS (ADR 0209) — a plugin is a directory in the plugins folder with a plugin.toml. A dialog
// plugin is a program the Plugins menu starts: the TUI draws a dialog over the page holding the
// plugin's latest frame, and passes it every key but Esc, which closes it. The two speak package
// plugin's protocol over the plugin's stdin and stdout.
//
// The dialog is the host's: the plugin only draws into it. A plugin that exits, fails to answer, or
// speaks another protocol costs its dialog and a toast, and nothing else.

const (
	pluginLogCap = 1 << 20 // a plugin's log, per start
	pluginQueue  = 256     // what the host sends, waiting for the plugin to read it
)

// loadPlugins lists the folder's plugins in the Plugins menu.
func (pl *plugins) loadPlugins() {
	pl.pluginList = discoverPlugins(pl.pluginOpt.Dir)
	pl.syncPluginEntries()
	pl.refreshPlugins()
	if pl.c.Attached() { // read anew, not at New: the program runs, and attach launched the first ones
		pl.launchServices()
	}
}

// openPlugin is a Plugins menu entry: the plugin's dialog, started, or brought forward when open.
func (pl *plugins) openPlugin(key string) {
	if key == "" {
		return // "no plugins yet"
	}
	if r := pl.running[key]; r != nil {
		r.show()
		return
	}
	i := slices.IndexFunc(pl.pluginList, func(e pluginEntry) bool { return e.key() == key })
	if i < 0 {
		pl.c.Notify("no plugin " + key)
		return
	}
	e := pl.pluginList[i]
	if e.reason != "" {
		pl.c.Notify(e.label())
		return
	}
	r, err := pl.startPlugin(e)
	if err != nil {
		pl.c.Failed(e.m.Name+" did not start", err)
		return
	}
	pl.running[key] = r
	pl.refreshPlugins() // a card shown takes no keys: a click or its command focuses it (§2.2)
}

// pluginTheme is the theme a plugin is sent: the one the screen wears.
func (pl *plugins) pluginTheme() plugin.Theme {
	colors, err := themes.Values(pl.c.Theme())
	if c, ok := pl.c.(interface {
		ThemeValues(string) (map[string]string, error)
	}); ok {
		colors, err = c.ThemeValues(pl.c.Theme())
	}
	if err != nil {
		colors = map[string]string{}
	}
	return plugin.Theme{Name: pl.c.Theme(), Colors: colors}
}

// themePlugins tells the open plugins the theme changed.
func (pl *plugins) themePlugins() {
	t := pl.pluginTheme()
	for _, r := range pl.running {
		r.send(plugin.MethodTheme, plugin.ThemeParams(t))
	}
}
