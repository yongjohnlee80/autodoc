package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// PLUGIN COMMANDS (ADR 1791268009 §2.5) — a manifest's [[commands]] are on the Plugins menu, under
// the plugin's own submenu, and on the SPC p card by their letters. Each is a command catalog entry
// (catalog.go), plugin.<name>.<id>, with no verb: the SPC p card is made from the catalog's, and F1
// lists them with their letters. A letter is the manifest's, or
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

// refreshPlugins rebuilds the Plugins menu and the SPC p card from the plugins found, the user's
// letters and what is running.
func (pl *plugins) refreshPlugins() {
	bindings := pl.bindPluginKeys()
	rows := make([]tuidecl.Row, 0, len(pl.pluginList))
	for _, e := range pl.pluginList {
		key := e.key()
		simple := e.reason != "" || (len(e.m.Commands) == 0 && !e.m.card() && !e.m.service())
		if simple {
			rows = append(rows, tuidecl.Row{"key": key, "kind": "item", "label": e.label(),
				"enabled": e.reason == "", "target": pluginTarget(key, targetOpen)})
			continue
		}
		sub := tuidecl.NewListModel("key", "label", "enabled", "target")
		var items []tuidecl.Row
		running := pl.running[key] != nil
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
	pl.pluginRows.Reset(rows)
}

// pluginCommandRef is a plugin command's catalog entry, as SPC p binds it: its plugin and its
// manifest's id and letter.
type pluginCommandRef struct {
	plugin, key, id, letter string
}

// syncPluginEntries makes the runnable plugins' [[commands]] the catalog's plugin entries,
// plugin.<name>.<id>, with no verb: SPC p binds them, F1 lists them, and they run their plugin's
// command. The catalog follows the folder: a plugin removed, or one that cannot run, has none.
func (pl *plugins) syncPluginEntries() {
	if !pl.c.HasCatalog() {
		return // before the registry is built: options syncs them
	}
	pl.c.RemovePluginEntries()
	pl.pluginCmds = map[string]pluginCommandRef{}
	for _, e := range pl.pluginList {
		if e.reason != "" {
			continue
		}
		title := cmp.Or(e.m.Title, e.m.Name)
		for _, c := range e.m.Commands {
			id, key, cmd := "plugin."+e.m.Name+"."+c.ID, e.key(), c.ID
			err := pl.c.AddPluginEntry(Entry{ID: id, Source: SourcePlugin, Owner: e.m.Name, Title: title + ": " + c.Title, Help: c.Title,
				Run: func(Invocation) error { pl.runPluginCommand(key, cmd); return nil }})
			if err != nil { // a manifest's ids are checked when it is read; this is a bug, said
				pl.c.Keep(err)
				continue
			}
			pl.pluginCmds[id] = pluginCommandRef{plugin: e.m.Name, key: key, id: cmd, letter: c.Key}
		}
	}
}

// bindPluginKeys gives the catalog's plugin commands their SPC p letters, in the order of the
// plugins' names, and sets the card: its letters, and its text. It returns each command's binding,
// by "<plugin>.<id>".
func (pl *plugins) bindPluginKeys() map[string]pluginBinding {
	var entries []*Entry
	entries = pl.c.PluginEntries()
	slices.SortStableFunc(entries, func(a, b *Entry) int { return strings.Compare(a.Owner, b.Owner) })
	out := map[string]pluginBinding{}
	taken := map[string]string{"q": "close card"}
	var rows []tuidecl.Row
	targets := map[string]string{}
	pl.pluginLetters = map[string][]string{}
	var bound, unbound []string
	for _, e := range entries {
		ref := pl.pluginCmds[e.ID]
		letter, chosen := pl.c.Prefs().pluginKeys[ref.plugin+"."+ref.id]
		if !chosen {
			letter = ref.letter
		}
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
			taken[letter] = ref.plugin
			target := pluginTarget(ref.key, targetCommand, ref.id)
			rows = append(rows, tuidecl.Row{"key": letter, "target": target})
			targets[letter] = target
			bound = append(bound, letter+"  "+e.Title)
			pl.pluginLetters[e.ID] = []string{"SPC p " + letter}
		}
		if b.letter == "" {
			unbound = append(unbound, "   "+e.Title+" — "+b.why)
		}
		out[ref.plugin+"."+ref.id] = b
	}
	pl.pluginKeyTargets = targets
	pl.pluginKeyRows.Reset(rows)
	text := "no plugin commands yet"
	if lines := append(bound, unbound...); len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	if pl.c.Attached() { // at New there is no program yet: attach's launchServices refreshes it
		pl.c.Set("App.pluginKeysText", text)
		pl.c.CommandsChanged() // F1 names the letters
	}
	return out
}

func (pl *plugins) pluginKey(letter string) {
	if target := pl.pluginKeyTargets[letter]; target != "" {
		pl.c.Close("pluginKeys")
		pl.pluginEntry(target)
	}
}

// pluginEntry is a Plugins menu row or a SPC p letter, chosen: its target.
func (pl *plugins) pluginEntry(target string) {
	key, rest, _ := strings.Cut(target, "|")
	action, id, _ := strings.Cut(rest, "|")
	switch action {
	case targetOpen:
		pl.openPlugin(key)
	case targetClose:
		if r := pl.running[key]; r != nil {
			r.close("")
		}
	case targetCommand:
		pl.runPluginCommand(key, id)
	}
}

// runPluginCommand runs a plugin's command: the plugin started first when it is not running, a
// card focused, and the command sent once the plugin has opened.
func (pl *plugins) runPluginCommand(key, id string) {
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
	r := pl.running[key]
	if e.m.card() && id == pluginToggle && r != nil {
		r.close("")
		return
	}
	if r == nil {
		var err error
		if r, err = pl.startPlugin(e); err != nil {
			pl.c.Failed(e.m.Name+" did not start", err)
			return
		}
		pl.running[key] = r
		pl.refreshPlugins()
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
func (pl *plugins) launchServices() {
	for _, e := range pl.pluginList {
		if e.reason != "" || !e.m.service() || e.m.Start != "launch" || pl.running[e.key()] != nil {
			continue
		}
		r, err := pl.startPlugin(e)
		if err != nil {
			pl.c.Failed(e.m.Name+" did not start", err)
			continue
		}
		pl.running[e.key()] = r
	}
	pl.refreshPlugins()
}
