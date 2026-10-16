package tui

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// THE SPC p CHOOSER (ADR 1792132889) — SPC p lists the plugins whose commands can run, each with the
// letter the user gave it, if any; a plugin's letter, or j/k then Enter, shows its card: its commands
// by their letters (plugincmds.go). * asks for a letter for the plugin under ›, and the next key is
// it (Backspace removes it), as AutoVim's Ctrl+g modal binds a destination. The letters are the
// user's alone, so a plugin installed never takes or moves one; a command's letter is unique within
// its plugin only.
//
// The chooser is a card, as the leader's is: text, with nothing focusable in it. A focused list takes
// j, k, g, G and Enter before a dialog's Shortcuts see them, so a letter it moves by could be given
// to a plugin and never reach it. On the card every key is a Shortcut, and the host keeps the cursor.

// pluginRow is a plugin on the SPC p chooser: one with commands that can run.
type pluginRow struct {
	key, title  string
	letter, why string // its letter, the user's; or, the user having given it one, why it is not
	commands    int
}

// pluginReserved are the chooser's own letters: what each does, as a refusal says it.
var pluginReserved = map[string]string{"j": "moves down", "k": "moves up", "q": "closes the chooser"}

// bindPluginLetters lists the plugins with commands, by name, each with its letter. Two wanting one
// (a preference edited by hand): the first by name keeps it, and the other says why it has none.
func (pl *plugins) bindPluginLetters() {
	var rows []pluginRow
	held := map[string]string{}
	for _, e := range pl.pluginList {
		if e.reason != "" || len(e.m.Commands) == 0 {
			continue
		}
		rows = append(rows, pluginRow{key: e.key(), title: cmp.Or(e.m.Title, e.m.Name), commands: len(e.m.Commands)})
	}
	slices.SortFunc(rows, func(a, b pluginRow) int { return strings.Compare(a.key, b.key) })
	for i := range rows {
		r := &rows[i]
		l := pl.c.Prefs().pluginLetter[r.key]
		switch {
		case l == "":
		case !pluginLetter(l):
			r.why = fmt.Sprintf("letter %q: want one letter or digit", l)
		case pluginReserved[l] != "":
			r.why = fmt.Sprintf("%s %s", l, pluginReserved[l])
		case held[l] != "":
			r.why = fmt.Sprintf("%s is %s's", l, held[l])
		default:
			r.letter, held[l] = l, r.title
		}
	}
	pl.chooser = rows
	pl.chooserAt = max(min(pl.chooserAt, len(rows)-1), 0)
	if pl.c.Attached() {
		pl.showChooser()
	}
}

// cardKeys is how the keyboard reaches a plugin's card: SPC p and its letter, or SPC p then the
// plugin chosen by the cursor.
func (pl *plugins) cardKeys(key string) string {
	if i := slices.IndexFunc(pl.chooser, func(r pluginRow) bool { return r.key == key }); i >= 0 && pl.chooser[i].letter != "" {
		return "SPC p " + pl.chooser[i].letter
	}
	return "SPC p › " + pl.pluginTitle(key)
}

// pluginTitle is a plugin's title, or its key when it has none (or is gone).
func (pl *plugins) pluginTitle(key string) string {
	if i := slices.IndexFunc(pl.pluginList, func(e pluginEntry) bool { return e.key() == key }); i >= 0 {
		return cmp.Or(pl.pluginList[i].m.Title, pl.pluginList[i].m.Name)
	}
	return key
}

// The chooser's help: as it waits for a plugin, and as * waits for a letter.
const (
	chooserHelp     = "a plugin's letter, or Enter, shows its commands · j/k move · * gives the plugin under › a letter · q or Esc closes"
	chooserBindHelp = "a letter or digit for %s (not j, k or q) · Backspace removes its letter · Esc closes"
)

// showChooser sets the chooser's text, a line per plugin, and its help.
func (pl *plugins) showChooser() {
	text := "no plugin has commands yet: Plugins › Add from a git URL… adds one"
	if len(pl.chooser) > 0 {
		width := 0
		for _, r := range pl.chooser {
			width = max(width, len([]rune(r.title)))
		}
		lines := make([]string, len(pl.chooser))
		for i, r := range pl.chooser {
			mark, letter := "  ", cmp.Or(r.letter, " ")
			if i == pl.chooserAt {
				mark = "› "
			}
			n := fmt.Sprintf("%d commands", r.commands)
			if r.commands == 1 {
				n = "1 command"
			}
			lines[i] = fmt.Sprintf("%s%s  %-*s  %s", mark, letter, width, r.title, n)
			if r.why != "" {
				lines[i] += " — " + r.why
			}
		}
		text = strings.Join(lines, "\n")
	}
	help := chooserHelp
	switch {
	case pl.chooserBind != "":
		help = fmt.Sprintf(chooserBindHelp, pl.pluginTitle(pl.chooserBind))
	case pl.chooserNote != "":
		help = pl.chooserNote + " · " + chooserHelp
	}
	pl.c.Set("App.pluginChooserText", text)
	pl.c.Set("App.pluginChooserHelp", help)
}

// openPluginChooser is SPC p: the chooser, waiting for a plugin.
func (pl *plugins) openPluginChooser() {
	pl.chooserBind, pl.chooserNote = "", ""
	pl.showChooser()
	pl.c.Open("pluginChooser")
}

// pluginChooserKey is a key on the chooser, by its Shortcut's name: a letter or digit, Up, Down,
// Enter, * or Backspace. After *, it is the letter asked for.
func (pl *plugins) pluginChooserKey(k string) {
	pl.chooserNote = ""
	defer func() {
		if pl.c.Attached() {
			pl.showChooser()
		}
	}()
	if pl.chooserBind != "" {
		key := pl.chooserBind
		pl.chooserBind = ""
		pl.chooserNote = pl.bindPluginLetter(key, k)
		return
	}
	switch k {
	case "j", "Down":
		pl.chooserAt = min(pl.chooserAt+1, max(len(pl.chooser)-1, 0))
	case "k", "Up":
		pl.chooserAt = max(pl.chooserAt-1, 0)
	case "q":
		pl.c.Close("pluginChooser")
	case "Enter":
		if pl.chooserAt < len(pl.chooser) {
			pl.choosePlugin(pl.chooser[pl.chooserAt].key)
		}
	case "*":
		if pl.chooserAt < len(pl.chooser) {
			pl.chooserBind = pl.chooser[pl.chooserAt].key
		}
	default:
		if i := slices.IndexFunc(pl.chooser, func(r pluginRow) bool { return r.letter == k }); i >= 0 {
			pl.chooserAt = i
			pl.choosePlugin(pl.chooser[i].key)
		}
	}
}

// choosePlugin shows a plugin's card in the chooser's place: its commands, by their letters.
func (pl *plugins) choosePlugin(key string) {
	pl.cardPlugin = key
	pl.showPluginCard()
	pl.c.Close("pluginChooser")
	pl.c.Open("pluginKeys")
}

// pluginCardBack is Backspace on a plugin's card: back to the chooser.
func (pl *plugins) pluginCardBack() {
	pl.c.Close("pluginKeys")
	pl.openPluginChooser()
}

// bindPluginLetter is the chooser's *: k becomes the plugin's letter, and Backspace removes it. A
// letter another plugin has moves to this one, and the chooser says so (rationale §R2): every other
// plugin the preferences give it loses it, so the preferences name one plugin per letter. One of the
// chooser's own letters, or a key that is no letter or digit, is refused, saying why. It returns
// what the chooser says.
func (pl *plugins) bindPluginLetter(key, k string) string {
	title := pl.pluginTitle(key)
	switch {
	case k == "Backspace":
		pl.c.Keep(pl.c.SetPluginPref(prefPluginPrefix+key+prefPluginLetter, ""))
		return title + " has no letter"
	case pluginReserved[k] != "":
		return fmt.Sprintf("%s %s: * again for another letter", k, pluginReserved[k])
	case !pluginLetter(k):
		return "a plugin's letter is a letter or digit: * again for another"
	}
	var was []string
	for _, name := range slices.Sorted(maps.Keys(pl.c.Prefs().pluginLetter)) {
		if name != key && pl.c.Prefs().pluginLetter[name] == k {
			pl.c.Keep(pl.c.SetPluginPref(prefPluginPrefix+name+prefPluginLetter, ""))
			was = append(was, pl.pluginTitle(name))
		}
	}
	pl.c.Keep(pl.c.SetPluginPref(prefPluginPrefix+key+prefPluginLetter, k))
	note := fmt.Sprintf("%s is %s's", k, title)
	if len(was) > 0 {
		note += fmt.Sprintf(" (it was %s's)", strings.Join(was, "'s and "))
	}
	return note
}
