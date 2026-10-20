package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/plugin"
)

// PLUGIN COMMANDS (ADR 1791268009 §2.5, ADR 1792132889) — a manifest's [[commands]] are on the
// Plugins menu, under the plugin's own submenu, and on the plugin's SPC p card by their letters: SPC p
// chooses the plugin (pluginpick.go), then its card shows its commands. Each is a command catalog
// entry (catalog.go), plugin.<name>.<id>, with no verb: the cards are made from the catalog's, and F1
// lists them with their keys. A letter is the manifest's, or the user's (tui.plugin.<name>.key.<id>;
// "" unbinds it), unique within its plugin only: two plugins may both use s. Two of one plugin's
// commands wanting a letter, the first in the manifest keeps it, and the other is listed unbound,
// saying why. Plugins never take AutoDoc's own leader letters: SPC p is a card of its own.
//
// A command starts its plugin when it is not running, and is sent once the plugin has opened. A
// card's commands open its panel and focus it, but its "toggle", which is the host's own: it starts
// the card, its panel open and focused; it shows a card whose panel was closed (the plugin ran on);
// and it hides a card whose panel is open, its plugin running on, signed in (ADR 1792300990 §4.1).
// A focused card takes every key, SPC included, so a toggle that only hid a focused card could never
// be reached from the keyboard. Plugins › Turn off is what stops a plugin.

// The targets of the Plugins menu's rows and the SPC p cards' letters: "<key>|open",
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

// pluginBinding is a command's place on its plugin's SPC p card: its letter, or why it has none.
type pluginBinding struct {
	letter, why string
}

// pluginCard is one plugin's SPC p card: its letters' targets, and its text.
type pluginCard struct {
	targets map[string]string
	text    string
}

// refreshPlugins rebuilds the Plugins menu, the SPC p chooser and the plugins' cards from the
// plugins found, the user's letters and what is running.
func (pl *plugins) refreshPlugins() {
	defer pl.c.WidgetsChanged() // a run started or stopped: a hidden card's dock icon comes or goes
	pl.bindPluginLetters()
	bindings := pl.bindPluginKeys()
	rows := make([]tuidecl.Row, 0, len(pl.pluginList))
	for _, e := range pl.pluginList {
		key := e.key()
		simple := e.reason != "" || (len(e.m.Commands) == 0 && !e.m.Card() && !e.m.Service())
		if simple {
			rows = append(rows, tuidecl.Row{"key": key, "kind": "item", "label": e.label(),
				"enabled": e.reason == "", "target": pluginTarget(key, targetOpen)})
			continue
		}
		sub := tuidecl.NewListModel("key", "label", "enabled", "target")
		var items []tuidecl.Row
		running := pl.running[key] != nil
		if !e.m.Service() {
			openLabel := "Open"
			if e.m.Card() {
				openLabel = "Show card"
				if running && pl.running[key].hidden {
					openLabel = "Reopen card"
				}
			}
			items = append(items, tuidecl.Row{"key": "open", "label": openLabel, "enabled": true,
				"target": pluginTarget(key, targetOpen)})
		}
		for _, c := range e.m.Commands {
			label := c.Title
			if b := bindings[e.m.Name+"."+c.ID]; b.letter != "" {
				label += " · " + pl.cardKeys(key) + " " + b.letter
			}
			items = append(items, tuidecl.Row{"key": "cmd:" + c.ID, "label": label, "enabled": true,
				"target": pluginTarget(key, targetCommand, c.ID)})
		}
		closeLabel := "Close"
		if e.m.Card() { // its panel's Close hides it; this stops its plugin
			closeLabel = "Turn off"
		}
		items = append(items, tuidecl.Row{"key": "close", "label": closeLabel, "enabled": running,
			"target": pluginTarget(key, targetClose)})
		sub.Reset(items)
		rows = append(rows, tuidecl.Row{"key": key, "kind": "submenu", "label": e.label(), "rows": sub})
	}
	if len(rows) == 0 {
		rows = []tuidecl.Row{{"key": "", "kind": "item", "label": "no plugins yet", "enabled": false, "target": ""}}
	}
	pl.pluginRows.Reset(rows)
}

// pluginCommandRef is a plugin command's catalog entry, as its SPC p card binds it: its plugin and
// its manifest's id and letter.
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

// bindPluginKeys gives the catalog's plugin commands their letters on their plugins' SPC p cards,
// unique within each plugin, and sets the cards: their letters, and their text. It returns each
// command's binding, by "<plugin>.<id>".
func (pl *plugins) bindPluginKeys() map[string]pluginBinding {
	entries := pl.c.PluginEntries() // each plugin's in its manifest's order
	slices.SortStableFunc(entries, func(a, b *Entry) int { return strings.Compare(a.Owner, b.Owner) })
	out := map[string]pluginBinding{}
	taken := map[string]map[string]string{} // by plugin: a letter's holder
	bound, unbound := map[string][]string{}, map[string][]string{}
	pl.pluginCards = map[string]*pluginCard{}
	pl.pluginLetters = map[string][]string{}
	for _, e := range entries {
		ref := pl.pluginCmds[e.ID]
		card := pl.pluginCards[ref.key]
		if card == nil {
			card = &pluginCard{targets: map[string]string{}}
			pl.pluginCards[ref.key] = card
			taken[ref.key] = map[string]string{"q": "closing the card"}
		}
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
		case taken[ref.key][letter] != "":
			b.why = fmt.Sprintf("%s is taken by %s", letter, taken[ref.key][letter])
		default:
			b.letter = letter
			taken[ref.key][letter] = e.Help
			card.targets[letter] = pluginTarget(ref.key, targetCommand, ref.id)
			bound[ref.key] = append(bound[ref.key], letter+"  "+e.Help)
			pl.pluginLetters[e.ID] = []string{pl.cardKeys(ref.key) + " " + letter}
		}
		if b.letter == "" {
			unbound[ref.key] = append(unbound[ref.key], "   "+e.Help+" — "+b.why)
		}
		out[ref.plugin+"."+ref.id] = b
	}
	for key, card := range pl.pluginCards {
		card.text = strings.Join(append(bound[key], unbound[key]...), "\n")
	}
	if pl.c.Attached() { // at New there is no program yet: attach's launchServices refreshes it
		pl.showChooser()
		pl.c.CommandsChanged() // F1 names the keys
	}
	return out
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
			r.stopFor(plugin.CloseOff, "")
		}
	case targetCommand:
		pl.runPluginCommand(key, id)
	}
}

// opener is the plugin whose [open] claims p's extension (openerOf); false when none does.
func (pl *plugins) opener(p string) (pluginEntry, bool) {
	if i := openerOf(pl.pluginList, kind.Ext(p)); i >= 0 {
		return pl.pluginList[i], true
	}
	return pluginEntry{}, false
}

// runPluginCommand runs a plugin's command: the plugin started first when it is not running, a
// card focused, and the command sent once the plugin has opened.
func (pl *plugins) runPluginCommand(key, id string) { pl.runPluginCommandArgs(key, id, nil) }

// runPluginCommandArgs is runPluginCommand with the command's arguments (protocol 7: a plugin's Lua
// package's autodoc.plugin.run); the caller refuses args for an older plugin.
func (pl *plugins) runPluginCommandArgs(key, id string, args []string) {
	i := slices.IndexFunc(pl.pluginList, func(e pluginEntry) bool { return e.key() == key })
	if i < 0 {
		pl.c.Notify(pl.c.Trf("autodoc.notice.plugincmds.no_plugin", key))
		return
	}
	e := pl.pluginList[i]
	if e.reason != "" {
		pl.c.Notify(e.label())
		return
	}
	r := pl.running[key]
	if e.m.Card() && id == pluginToggle && r != nil && !r.hidden {
		// the toggle hides a shown card, its plugin running on, signed in (ADR 1792300990 §4.1);
		// Plugins › Turn off is the way to stop it
		pl.hideCard(r)
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
		r.focusOnOpen = e.m.Card()
		if e.m.Card() {
			pl.showCard(r, true)
		}
	} else if e.m.Card() {
		pl.showCard(r, true)
	} else {
		r.show()
	}
	if !(e.m.Card() && id == pluginToggle) {
		r.send(plugin.MethodCommand, plugin.WithArgs(pl.commandParams(e, id), args))
	}
}

// commandParams are plugin.command id as e is sent it. A protocol-4 plugin on the feed is sent the
// note as it stands inside the command: the feed waits for a pause, and both queues drop a waiting
// document when a newer one comes, so a document sent beside the command could arrive late or not
// at all, and the command run on an older text, cursor or selection. One too large for the link
// goes as its too-large form (plugin.FitCommand). Earlier protocols are sent the id alone.
func (pl *plugins) commandParams(e pluginEntry, id string) []any {
	if e.m.Feed.Document && e.m.Protocol >= 4 {
		return plugin.FitCommand(id, pl.document())
	}
	return plugin.CommandParams(id)
}

// launchServices starts the services whose manifest says start = "launch": with the TUI, and again
// when the plugins are read anew (one added, say). A running one is left as it is.
func (pl *plugins) launchServices() {
	for _, e := range pl.pluginList {
		if e.reason != "" || !e.m.Service() || e.m.Start != "launch" || pl.running[e.key()] != nil {
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
