package tui

import (
	"fmt"
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
func (h *Host) loadPlugins() {
	h.pluginList = discoverPlugins(h.pluginOpt.Dir)
	h.syncPluginEntries()
	h.refreshPlugins()
	if h.p != nil { // read anew, not at New: the program runs, and attach launched the first ones
		h.launchServices()
	}
}

// openPlugin is a Plugins menu entry: the plugin's dialog, started, or brought forward when open.
func (h *Host) openPlugin(key string) {
	if key == "" {
		return // "no plugins yet"
	}
	if r := h.running[key]; r != nil {
		r.show()
		return
	}
	i := slices.IndexFunc(h.pluginList, func(e pluginEntry) bool { return e.key() == key })
	if i < 0 {
		h.notify("no plugin " + key)
		return
	}
	e := h.pluginList[i]
	if e.reason != "" {
		h.notify(e.label())
		return
	}
	r, err := h.startPlugin(e)
	if err != nil {
		h.notify(fmt.Sprintf("%s did not start: %v", e.m.Name, err))
		return
	}
	h.running[key] = r
	h.refreshPlugins() // a card shown takes no keys: a click or its command focuses it (§2.2)
}

// pluginTheme is the theme a plugin is sent: the one the screen wears.
func (h *Host) pluginTheme() plugin.Theme {
	colors, err := themes.Values(h.theme)
	if err != nil {
		colors = map[string]string{}
	}
	return plugin.Theme{Name: h.theme, Colors: colors}
}

// themePlugins tells the open plugins the theme changed.
func (h *Host) themePlugins() {
	t := h.pluginTheme()
	for _, r := range h.running {
		r.send(plugin.MethodTheme, plugin.ThemeParams(t))
	}
}

// ---------------------------------------------------------------- one plugin, running
