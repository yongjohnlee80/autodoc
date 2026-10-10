package tui

import (
	"fmt"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// PLUGIN COMMANDS (ADR 1791268009 §2.5) — a manifest's [[commands]] are on the Plugins menu, under
// the plugin's own submenu, and on the SPC p card by their letters. A letter is the manifest's, or
// the user's (tui.plugin.<name>.key.<id>; "" unbinds it); the first plugin by name keeps a letter
// two want, and the other is listed unbound, saying why. Plugins never take AutoDoc's own leader
// letters: SPC p is a card of its own.
//
// A command starts its plugin when it is not running, and is sent once the plugin has opened. A
// card's commands focus it, but its "toggle", which is the host's own: it opens the card, focused,
// and closes it when it is open. A focused card takes every key, SPC included, so a toggle that only
// closed a focused card could never be reached from the keyboard.

// The targets of the Plugins menu's rows and the SPC p card's letters: "<key>|open",
// "<key>|close" and "<key>|cmd|<id>".
const (
	targetOpen    = "open"
	targetClose   = "close"
	targetCommand = "cmd"
	pluginToggle  = "toggle" // a card's command the host runs itself: open (focused) or close
)

func pluginTarget(key string, parts ...string) string {
	return strings.Join(append([]string{key}, parts...), "|")
}

// pluginBinding is a command's place on the SPC p card: its letter, or why it has none.
type pluginBinding struct {
	letter, why string
}

// card is a dialog that does not hold the keys while open.
func (m *manifest) card() bool { return m.Kind == "dialog" && !m.modal() }

// refreshPlugins rebuilds the Plugins menu and the SPC p card from the plugins found, the user's
// letters and what is running.
func (h *Host) refreshPlugins() {
	bindings := h.bindPluginKeys()
	rows := make([]tuidecl.Row, 0, len(h.pluginList))
	for _, e := range h.pluginList {
		key := e.key()
		simple := e.reason != "" || (len(e.m.Commands) == 0 && !e.m.card() && !e.m.service())
		if simple {
			rows = append(rows, tuidecl.Row{"key": key, "kind": "item", "label": e.label(),
				"enabled": e.reason == "", "target": pluginTarget(key, targetOpen)})
			continue
		}
		sub := tuidecl.NewListModel("key", "label", "enabled", "target")
		var items []tuidecl.Row
		running := h.running[key] != nil
		if !e.m.service() {
			items = append(items, tuidecl.Row{"key": "open", "label": "Open", "enabled": true,
				"target": pluginTarget(key, targetOpen)})
		}
		for _, c := range e.m.Commands {
			label := c.Title
			if b := bindings[e.m.Name+"."+c.ID]; b.letter != "" {
				label += " · SPC p " + b.letter
			}
			items = append(items, tuidecl.Row{"key": "cmd:" + c.ID, "label": label, "enabled": true,
				"target": pluginTarget(key, targetCommand, c.ID)})
		}
		items = append(items, tuidecl.Row{"key": "close", "label": "Close", "enabled": running,
			"target": pluginTarget(key, targetClose)})
		sub.Reset(items)
		rows = append(rows, tuidecl.Row{"key": key, "kind": "submenu", "label": e.label(), "rows": sub})
	}
	if len(rows) == 0 {
		rows = []tuidecl.Row{{"key": "", "kind": "item", "label": "no plugins yet", "enabled": false, "target": ""}}
	}
	h.pluginRows.Reset(rows)
}

// bindPluginKeys gives each runnable plugin's commands their SPC p letters, in the order of the
// plugins' names, and sets the card: its letters, and its text. It returns each command's binding,
// by "<plugin>.<id>".
func (h *Host) bindPluginKeys() map[string]pluginBinding {
	entries := slices.Clone(h.pluginList)
	slices.SortStableFunc(entries, func(a, b pluginEntry) int { return strings.Compare(a.m.Name, b.m.Name) })
	out := map[string]pluginBinding{}
	taken := map[string]string{"q": "close card"}
	var rows []tuidecl.Row
	var bound, unbound []string
	for _, e := range entries {
		if e.reason != "" {
			continue
		}
		title := e.m.Title
		if title == "" {
			title = e.m.Name
		}
		for _, c := range e.m.Commands {
			letter, chosen := h.prefs.pluginKeys[e.m.Name+"."+c.ID]
			if !chosen {
				letter = c.Key
			}
			label := title + ": " + c.Title
			var b pluginBinding
			switch {
			case letter == "" && chosen:
				b.why = "unbound in the preferences"
			case letter == "":
				b.why = "no key"
			case !pluginLetter(letter):
				b.why = fmt.Sprintf("key %q: want one letter or digit", letter)
			case taken[letter] != "":
				b.why = fmt.Sprintf("%s is %s's", letter, taken[letter])
			default:
				b.letter = letter
				taken[letter] = e.m.Name
				rows = append(rows, tuidecl.Row{"key": letter, "target": pluginTarget(e.key(), targetCommand, c.ID)})
				bound = append(bound, letter+"  "+label)
			}
			if b.letter == "" {
				unbound = append(unbound, "   "+label+" — "+b.why)
			}
			out[e.m.Name+"."+c.ID] = b
		}
	}
	rows = append(rows, tuidecl.Row{"key": "q", "target": ""})
	h.pluginKeyRows.Reset(rows)
	text := "no plugin commands yet"
	if lines := append(bound, unbound...); len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	if h.p != nil { // at New there is no program yet: attach's launchServices refreshes it
		h.set("App.pluginKeysText", text)
	}
	return out
}

// pluginEntry is a Plugins menu row or a SPC p letter, chosen: its target.
func (h *Host) pluginEntry(target string) {
	key, rest, _ := strings.Cut(target, "|")
	action, id, _ := strings.Cut(rest, "|")
	switch action {
	case targetOpen:
		h.openPlugin(key)
	case targetClose:
		if r := h.running[key]; r != nil {
			r.close("")
		}
	case targetCommand:
		h.runPluginCommand(key, id)
	}
}

// runPluginCommand runs a plugin's command: the plugin started first when it is not running, a
// card focused, and the command sent once the plugin has opened.
func (h *Host) runPluginCommand(key, id string) {
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
	r := h.running[key]
	if e.m.card() && id == pluginToggle && r != nil {
		r.close("")
		return
	}
	if r == nil {
		var err error
		if r, err = h.startPlugin(e); err != nil {
			h.notify(fmt.Sprintf("%s did not start: %v", e.m.Name, err))
			return
		}
		h.running[key] = r
		h.refreshPlugins()
		r.focusOnOpen = e.m.card()
	} else {
		r.show()
		if e.m.card() && r.view != nil {
			r.view.RequestFocus()
		}
	}
	if !(e.m.card() && id == pluginToggle) {
		r.send(plugin.MethodCommand, plugin.CommandParams(id))
	}
}

// launchServices starts the services whose manifest says start = "launch": with the TUI, and again
// when the plugins are read anew (one added, say). A running one is left as it is.
func (h *Host) launchServices() {
	for _, e := range h.pluginList {
		if e.reason != "" || !e.m.service() || e.m.Start != "launch" || h.running[e.key()] != nil {
			continue
		}
		r, err := h.startPlugin(e)
		if err != nil {
			h.notify(fmt.Sprintf("%s did not start: %v", e.m.Name, err))
			continue
		}
		h.running[e.key()] = r
	}
	h.refreshPlugins()
}
