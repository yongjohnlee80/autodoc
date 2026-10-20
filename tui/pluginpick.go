package tui

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// THE SPC p CHOOSER (ADR 1792132889) — SPC p lists the plugins whose commands can run, each with the
// letter the user gave it, if any; a plugin's letter, or j/k then Enter, shows its card in the same
// dialog: its commands by their letters (plugincmds.go), Backspace back to the list. * asks for a letter for the plugin under ›, and the next key is
// it (Backspace removes it), as AutoVim's Ctrl+g modal binds a destination. The letters are the
// user's alone, so a plugin installed never takes or moves one; a command's letter is unique within
// its plugin only.
//
// The chooser is a card, as the leader's is: text, with nothing focusable in it. A focused list takes
// j, k, g, G and Enter before a dialog's Shortcuts see them, so a letter it moves by could be given
// to a plugin and never reach it. On the card every key is a Shortcut, and the host keeps the cursor.
// The list and a plugin's card are one dialog, its text the host's: closing one dialog and opening
// another from one key leaves the keyboard outside the one opened, which then never hears Esc.

// pluginRow is a plugin on the SPC p chooser: one with commands that can run.
type pluginRow struct {
	key, title  string
	letter, why string // its letter, the user's; or, the user having given it one, why it is not
	commands    int
}

// pluginReserved are the chooser's own letters, each why it is no plugin's by its catalog ids: on the
// list's row, and again as *'s refusal. A sentence each, as every language says it; %1 is the letter.
var pluginReserved = map[string][2]string{
	"j": {"autodoc.text.pluginpick.moves_down", "autodoc.text.pluginpick.moves_down_again_for_another_letter"},
	"k": {"autodoc.text.pluginpick.moves_up", "autodoc.text.pluginpick.moves_up_again_for_another_letter"},
	"q": {"autodoc.text.pluginpick.closes_the_chooser", "autodoc.text.pluginpick.closes_the_chooser_again_for_another_letter"},
}

// reservedLetter is whether k is one of the chooser's own letters.
func reservedLetter(k string) bool {
	_, ok := pluginReserved[k]
	return ok
}

// reservedWhy is why k, one of the chooser's own letters, is no plugin's: on the list's row, or, again,
// as *'s refusal.
func (pl *plugins) reservedWhy(k string, again bool) string {
	ids := pluginReserved[k]
	if again {
		return pl.c.Trf(ids[1], k)
	}
	return pl.c.Trf(ids[0], k)
}

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
			r.why = pl.c.Trf("autodoc.text.pluginpick.letter_want_one_letter_or_digit", strconv.Quote(l))
		case reservedLetter(l):
			r.why = pl.reservedWhy(l, false)
		case held[l] != "":
			r.why = pl.c.Trf("autodoc.text.pluginpick.is_s", l, held[l])
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
	return pl.c.Trf("autodoc.text.pluginpick.spc_p", pl.pluginTitle(key))
}

// pluginTitle is a plugin's title, or its key when it has none (or is gone).
func (pl *plugins) pluginTitle(key string) string {
	if i := slices.IndexFunc(pl.pluginList, func(e pluginEntry) bool { return e.key() == key }); i >= 0 {
		return cmp.Or(pl.pluginList[i].m.Title, pl.pluginList[i].m.Name)
	}
	return key
}

// chooserHelp is the chooser's help as it waits for a plugin, in English, before the program; then
// showChooser's, in the UI's language, as it is for * waiting for a letter and on a plugin's card.
const chooserHelp = "a plugin's letter, or Enter, shows its commands · j/k move · * gives the plugin under › a letter · q or Esc closes"

// showChooser sets the dialog: the chosen plugin's card, or the list.
func (pl *plugins) showChooser() {
	if pl.cardPlugin != "" {
		text := pl.c.Tr("autodoc.text.pluginpick.no_commands_the_plugin_is_gone_or_cannot_run")
		if card := pl.pluginCards[pl.cardPlugin]; card != nil {
			text = card.text
		}
		pl.c.Set("App.pluginChooserTitle", pl.c.Trf("autodoc.text.pluginpick.spc_p", pl.pluginTitle(pl.cardPlugin)))
		pl.c.Set("App.pluginChooserText", text)
		pl.c.Set("App.pluginChooserHelp", pl.c.Tr("autodoc.text.pluginpick.a_key_runs_its_command_backspace_goes_back_q_or"))
		return
	}
	text := pl.c.Tr("autodoc.text.pluginpick.no_plugin_has_commands_yet_plugins_add_from_a_gi")
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
			n := pl.c.Trf("autodoc.text.pluginpick.commands", r.commands)
			if r.commands == 1 {
				n = pl.c.Tr("autodoc.text.pluginpick.1_command")
			}
			lines[i] = fmt.Sprintf("%s%s  %-*s  %s", mark, letter, width, r.title, n)
			if r.why != "" {
				lines[i] += " — " + r.why
			}
		}
		text = strings.Join(lines, "\n")
	}
	help := pl.c.Tr("autodoc.text.pluginpick.a_plugin_s_letter_or_enter_shows_its_commands_j")
	switch {
	case pl.chooserBind != "":
		help = pl.c.Trf("autodoc.text.pluginpick.a_letter_or_digit_for_not_j_k_or_q_backspace_rem", pl.pluginTitle(pl.chooserBind))
	case pl.chooserNote != "":
		help = pl.chooserNote + " · " + help
	}
	pl.c.Set("App.pluginChooserTitle", pl.c.Tr("autodoc.text.pluginpick.spc_p_plugins"))
	pl.c.Set("App.pluginChooserText", text)
	pl.c.Set("App.pluginChooserHelp", help)
}

// openPluginChooser is SPC p: the list, waiting for a plugin.
func (pl *plugins) openPluginChooser() {
	pl.chooserBind, pl.chooserNote, pl.cardPlugin = "", "", ""
	pl.showChooser()
	pl.c.Open("pluginChooser")
}

// pluginChooserKey is a key on the dialog, by its Shortcut's name: a letter or digit, Up, Down,
// Enter, * or Backspace. On a plugin's card, a letter runs its command and Backspace goes back to the
// list; on the list, after *, it is the letter asked for.
func (pl *plugins) pluginChooserKey(k string) {
	pl.chooserNote = ""
	defer func() {
		if pl.c.Attached() {
			pl.showChooser()
		}
	}()
	if pl.cardPlugin != "" {
		pl.pluginCardKey(k)
		return
	}
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

// choosePlugin shows a plugin's card in the list's place: its commands, by their letters.
func (pl *plugins) choosePlugin(key string) { pl.cardPlugin = key }

// pluginCardKey is a key on a plugin's card: a letter runs its command once the dialog is closed;
// Backspace goes back to the list, its cursor where it was.
func (pl *plugins) pluginCardKey(k string) {
	switch k {
	case "Backspace":
		pl.cardPlugin = ""
	case "q":
		pl.c.Close("pluginChooser")
	default:
		if card := pl.pluginCards[pl.cardPlugin]; card != nil && card.targets[k] != "" {
			pl.c.Close("pluginChooser")
			pl.pluginEntry(card.targets[k])
		}
	}
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
		return pl.c.Trf("autodoc.text.pluginpick.has_no_letter", title)
	case reservedLetter(k):
		return pl.reservedWhy(k, true)
	case !pluginLetter(k):
		return pl.c.Tr("autodoc.text.pluginpick.a_plugin_s_letter_is_a_letter_or_digit_again_for")
	}
	var was []string
	for _, name := range slices.Sorted(maps.Keys(pl.c.Prefs().pluginLetter)) {
		if name != key && pl.c.Prefs().pluginLetter[name] == k {
			pl.c.Keep(pl.c.SetPluginPref(prefPluginPrefix+name+prefPluginLetter, ""))
			was = append(was, pl.pluginTitle(name))
		}
	}
	pl.c.Keep(pl.c.SetPluginPref(prefPluginPrefix+key+prefPluginLetter, k))
	if len(was) > 0 {
		return pl.c.Trf("autodoc.text.pluginpick.is_s_it_was_s", k, title, strings.Join(was, pl.c.Tr("autodoc.text.pluginpick.s_and")))
	}
	return pl.c.Trf("autodoc.text.pluginpick.is_s", k, title)
}
