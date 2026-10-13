package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// KEYS — every key the Window answers, and every card of keys (SPC's, and a card a key on it
// opens), declared once. From this catalog come main.qml's Window shortcuts and its cards'
// declarations, each card's QML (views/<View>.qml), its text, and the cards as help shows them.
// The QML is generated: TestKeysAreGenerated fails when a file differs from the catalog, and
// `go test ./tui -run TestKeysAreGenerated -update` rewrites them. A feature adds keys and cards
// with the Keyed capability.
//
// A key runs exactly one thing: Run's QML, the card it Opens, or Cmd, a command catalog entry by its
// id (catalog.go), which runs as App.run("<id>") and is named on its card by the entry's title
// when the key says nothing itself. Cmd names a built-in or a feature's entry only: those are
// fixed when the registry is built, which checks every key, while Lua's and the plugins' come and
// go (a Lua command is keyed by autodoc.map, a plugin's on SPC p). The host's own keys below keep
// Run: every registry checks them, a test's of features alone too, where no built-in entry is.

// Key is one key.
type Key struct {
	// Card is where it is answered: "" the Window itself, "leader" SPC's card, or the ID of a card a
	// key opens.
	Card string
	Seq  string   // the Shortcut's sequence: "Ctrl+G", "Shift+W", "`"
	Also []string // more sequences that do the same, shown nowhere: SPC SPC beside SPC /
	Show string   // how a card names it ("W"), or how help names a Window key ("Ctrl+h/j/k/l")
	Does string   // what it does, as its card says it; "" for a key no card lists
	Run  string   // the QML it runs: "App.openSearch()", "help.open()"
	// Opens is the card the key opens, in place of Run: its card closes and that one opens.
	Opens string
	// Cmd is a catalog entry's id, in place of Run and Opens: the key runs App.run("<id>"), and a
	// card names it by the entry's title when Does is "".
	Cmd string
	// Group is the heading its card lists it under, when the card has Groups: "FIND & OPEN".
	Group string
	// Scope is the pane it is live in, by that pane's FocusScope in main.qml (keyScopes): a key of
	// the Window's ("" Card) answered only while the keyboard is in it, and there before the
	// Window's own key of that sequence. "" is everywhere.
	Scope string
}

// keyScope is a pane whose keys are its own: a FocusScope in main.qml holding a generated block of
// them, at indent.
type keyScope struct {
	ID, Indent string
}

// keyScopes are the panes with keys of their own: the page (the Editor's FocusScope), the todos'
// panel (its keys the todos feature's), and the
// explorer's and the relations' panels.
var keyScopes = []keyScope{{"page", "            "}, {"explorer", "        "}, {"links", "        "}, {"todos", "                "}}

// findKeys are / n N in each pane find searches that has keys of its own (FindPane.Scope), in the
// features' order (which is find's, the registry keeping it): the pane answers them itself (find.go). The Window's own / n N answer everywhere else,
// saying where to put the keyboard.
func findKeys(fs []Feature) []Key {
	var out []Key
	for _, f := range fs {
		p, ok := f.(Findable)
		if !ok {
			continue
		}
		scope := p.FindPane().Scope
		if scope == "" {
			continue
		}
		out = append(out,
			Key{Scope: scope, Seq: "/", Show: "/", Run: "App.openFind()"},
			Key{Scope: scope, Seq: "n", Show: "n and N", Run: "App.findNext()"},
			Key{Scope: scope, Seq: "Shift+N", Show: "n and N", Run: "App.findPrevious()"})
	}
	return out
}

// scopedKeys are the other keys a pane answers itself: d in the Relations drawer shows the second
// ring, or hides it (relations.go).
var scopedKeys = []Key{{Scope: "links", Seq: "d", Show: "d in the relations", Run: "App.relationsDepth()"}}

// Card is a card of keys: a Dialog listing its keys, each closing the card before it runs.
type Card struct {
	ID    string // the Dialog's id, a lower-camel name no other id of main.qml has: "leader"
	View  string // its file and type, views/<View>.qml, an upper-camel name: "Leader"
	Title string // its frame's title: "SPC — commands"
	// Back is the card Backspace returns to, and the only card a key opening it may be on; "" for
	// a card the Window's keys open (SPC's).
	Back string
	// Help is how help heads the card's list: "THE LEADER CARD (…)".
	Help string
	// Groups are the headings its keys are listed under, a column of them each, left to right; a
	// key whose Group none is goes under MORE, last. nil lists the keys two a row, unheaded.
	Groups [][]string
}

// Keyed is a feature with keys of its own, and cards of its own for keys to open.
type Keyed interface {
	Keys() []Key
	Cards() []Card
}

// leader is SPC's card: Space in Vim's Normal mode, Ctrl+Space in every editor mode.
const leader = "leader"

// cards are the host's cards.
var cards = []Card{{ID: leader, View: "Leader", Title: "SPC — commands",
	Help: "THE LEADER CARD (Space in Normal mode, Ctrl+Space in every mode)", Groups: leaderGroups}}

// leaderGroups are SPC's card's columns. The card is to fit the smallest screen, main.qml's 80 × 20:
// its frame leaves the body 76 cells by 16 rows. MANAGE and WINDOW, the widest groups, share a
// column, and the narrowest has room for the agents' key and a feature's under MORE.
var leaderGroups = [][]string{{groupFind, groupFile}, {groupPanels, groupAgents, groupHelp}, {groupManage, groupWindow}}

// windowKeys fire whichever widget has focus, when it leaves them. Space and Ctrl+h/j/k/l reach the
// Window from the page only in Vim's Normal mode: Insert mode, and the Text editor mode, type them.
var windowKeys = []Key{
	{Seq: "Space", Show: "Space", Opens: leader},
	{Seq: "Ctrl+Space", Show: "Ctrl+Space", Opens: leader}, // the leader in every editor mode
	{Seq: "Ctrl+H", Show: "Ctrl+h/j/k/l", Run: `App.movePane("h")`},
	{Seq: "Ctrl+J", Show: "Ctrl+h/j/k/l", Run: `App.movePane("j")`},
	{Seq: "Ctrl+K", Show: "Ctrl+h/j/k/l", Run: `App.movePane("k")`},
	{Seq: "Ctrl+L", Show: "Ctrl+h/j/k/l", Run: `App.movePane("l")`},
	{Seq: "Ctrl+S", Show: "Ctrl+S", Run: "App.save()"},
	{Seq: "Ctrl+O", Show: "Ctrl+O", Run: "App.openPicker()"},
	{Seq: "Ctrl+N", Show: "Ctrl+N", Run: "App.newFile()"},
	// as vim's search; the Vim keyset takes Ctrl+F and Ctrl+B to page
	{Seq: "Ctrl+G", Show: "Ctrl+G", Run: "App.openSearch()"},
	// the command line in every editor mode, on : with Ctrl: the Text mode types :; a terminal sends
	// Ctrl+; only with kitty's keyboard protocol, so Search › Command line… is always there
	{Seq: "Ctrl+;", Show: "Ctrl+;", Run: "App.openCommandLine()"},
	// / n N in a pane find does not cover: where to put the keyboard (find.go); in the panes it
	// covers, their own (findKeys) win
	{Seq: "/", Show: "/", Run: "App.openFind()"},
	{Seq: "n", Show: "n and N", Run: "App.findNext()"},
	{Seq: "Shift+N", Show: "n and N", Run: "App.findPrevious()"},
	{Seq: "Ctrl+W", Show: "Ctrl+W", Run: "App.pickWorkspace()"},
	{Seq: "Ctrl+Q", Show: "Ctrl+Q", Run: "App.quit()"},
	{Seq: "F1", Show: "F1", Run: "help.open()"},
	// the window's zoom (fonts.go); nothing in a terminal
	{Seq: "Ctrl+=", Show: "Ctrl+= Ctrl+- Ctrl+0", Run: `App.zoomWindow("in")`},
	{Seq: "Ctrl+-", Show: "Ctrl+= Ctrl+- Ctrl+0", Run: `App.zoomWindow("out")`},
	{Seq: "Ctrl+0", Show: "Ctrl+= Ctrl+- Ctrl+0", Run: "App.setZoom(100)"},
	// the Vim keys' card, in Normal mode (the Text mode types it); ? again closes it
	{Seq: "?", Show: "?", Run: "App.toggleVimKeys()"},
}

// The headings of SPC's card, by purpose. A feature's key on SPC's card names one in Group, the
// agents' groupAgents, or lands under MORE.
const (
	groupFind   = "FIND & OPEN"
	groupFile   = "THIS FILE"
	groupPanels = "PANELS"
	groupAgents = "AGENTS"
	groupHelp   = "HELP"
	groupManage = "MANAGE"
	groupWindow = "WINDOW"
	groupMore   = "MORE"
)

// leaderKeys are SPC's card's, by group, in the order each lists them.
var leaderKeys = onCard(leader, slices.Concat(
	inGroup(groupFind, []Key{
		{Seq: "/", Also: []string{"Space"}, Show: "/", Does: "search (or SPC again)", Run: "App.openSearch()"},
		{Seq: "o", Show: "o", Does: "open a document", Run: "App.openPicker()"},
		{Seq: "f", Show: "f", Does: "open a file anywhere", Run: "App.openFileDialog()"},
		{Seq: "r", Show: "r", Does: "recent files", Run: "App.openRecent()"},
		// no comma: with it SPC's card is 77 cells across, one more than the screen leaves it
		{Seq: "j", Show: "j", Does: "related files by digit", Run: "App.openJumpCard()"},
		{Seq: "b", Show: "b", Does: "back", Run: "App.goBack()"},
	}),
	inGroup(groupFile, []Key{
		{Seq: "n", Show: "n", Does: "new file", Run: "App.newFile()"},
		{Seq: "Shift+O", Show: "O", Does: "the file's outline", Run: "App.openOutline()"},
		{Seq: "d", Show: "d", Does: "view the diagram", Run: "App.previewDiagram()"},
		{Seq: "Shift+H", Show: "H", Does: "preview HTML", Run: "App.previewHTML()"},
	}),
	inGroup(groupPanels, []Key{
		{Seq: "e", Show: "e", Does: "the explorer", Run: "App.toggleExplorer()"},
		{Seq: "l", Show: "l", Does: "the relations", Run: "App.toggleRelations()"},
		{Seq: "`", Show: "`", Does: "the terminal", Run: "App.toggleTerminal()"},
		{Seq: "x", Show: "x", Does: "todos", Run: "App.openTodos()"},
		{Seq: "h", Show: "h", Does: "notifications", Run: "App.openNotices()"},
	}),
	// a is the agents' card's, which their feature brings
	inGroup(groupAgents, []Key{
		{Seq: "g", Show: "g", Does: "AI agent", Run: "App.toggleAgent()"},
		{Seq: "Shift+G", Show: "G", Does: "agent profiles", Run: "App.openAgentProfiles()"},
	}),
	inGroup(groupHelp, []Key{
		{Seq: "?", Show: "?", Does: "help", Run: "help.open()"},
		{Seq: "Shift+A", Show: "A", Does: "about", Run: "about.open()"},
		{Seq: "Shift+Q", Show: "Q", Does: "quit", Run: "App.quit()"},
	}),
	inGroup(groupManage, []Key{
		{Seq: "w", Show: "w", Does: "switch workspace", Run: "App.pickWorkspace()"},
		{Seq: "Shift+W", Show: "W", Does: "manage workspaces", Run: "App.manageWorkspaces()"},
		{Seq: "s", Show: "s", Does: "search models", Run: "App.openAIModels()"},
		{Seq: ",", Show: ",", Does: "editor preferences", Run: "App.openPrefs()"},
		{Seq: "k", Show: "k", Does: "the editor mode (Vim, Text)", Run: "App.toggleKeymap()"},
	}),
	inGroup(groupWindow, []Key{
		{Seq: "m", Show: "m", Does: "show/focus or hide the menu", Run: "App.leaderMenuBar()"},
		{Seq: ".", Show: ".", Does: "the right-click menu", Run: "App.openContextMenu()"},
		{Seq: "t", Show: "t", Does: "the status line", Run: "App.toggleStatusLine()"},
		{Seq: "Shift+L", Show: "L", Does: "arrange a panel", Run: "App.arrangePanel()"},
		// SPC p is the plugins' (pluginfeature.go: Keys)
	}),
))

// inGroup is keys, each under group.
func inGroup(group string, keys []Key) []Key {
	for i := range keys {
		keys[i].Group = group
	}
	return keys
}

// onCard is keys, each on card.
func onCard(card string, keys []Key) []Key {
	for i := range keys {
		keys[i].Card = card
	}
	return keys
}

// keyset is the catalog: every card, and every key, the host's first and then each feature's.
type keyset struct {
	cards []Card
	keys  []Key
}

// keyCatalog is the catalog with every feature's keys and cards after the host's. A key running a
// catalog entry that says nothing itself is named by the entry's title.
func keyCatalog(fs []Feature) keyset {
	k := keyset{cards: slices.Clone(cards), keys: slices.Concat(windowKeys, findKeys(fs), scopedKeys, leaderKeys)}
	titles := map[string]string{}
	for _, f := range fs {
		if kf, ok := f.(Keyed); ok {
			k.cards, k.keys = append(k.cards, kf.Cards()...), append(k.keys, kf.Keys()...)
		}
		if c, ok := f.(Cataloged); ok {
			for _, e := range c.Catalog() {
				titles[e.ID] = e.Title
			}
		}
	}
	for i, key := range k.keys {
		if key.Cmd != "" && key.Does == "" && key.Card != "" {
			k.keys[i].Does = titles[key.Cmd]
		}
	}
	return k
}

// on are the keys on card ("" the Window's own, everywhere).
func (k keyset) on(card string) []Key {
	var out []Key
	for _, key := range k.keys {
		if key.Card == card && key.Scope == "" {
			out = append(out, key)
		}
	}
	return out
}

// in are the keys of the pane scope.
func (k keyset) in(scope string) []Key {
	var out []Key
	for _, key := range k.keys {
		if key.Scope == scope {
			out = append(out, key)
		}
	}
	return out
}

// card is the card of id.
func (k keyset) card(id string) (Card, bool) {
	i := slices.IndexFunc(k.cards, func(c Card) bool { return c.ID == id })
	if i < 0 {
		return Card{}, false
	}
	return k.cards[i], true
}

// reserved are the sequences every card answers itself: q and Escape close it.
var reserved = []string{"q", "Escape"}

// check refuses:
//   - a card declared twice, whose ID or View is no identifier, or whose ID is one of taken (the
//     other ids of main.qml);
//   - a key on, or opening, a card that is not declared, and a card nothing opens;
//   - a key opening a card from anywhere but the card's Back (the Window, for a card with none), so
//     Backspace goes back where the card came from;
//   - a sequence two keys of one place take, or one a card answers itself: q, Escape, and Backspace
//     on a card with a Back;
//   - a key running none, or more than one, of Run, Opens and Cmd; and a Cmd that is no built-in or
//     feature's entry of cat.
func (k keyset) check(taken map[string]bool, cat *catalog) error {
	var errs []error
	opened := map[string]bool{}
	for i, c := range k.cards {
		switch {
		case slices.ContainsFunc(k.cards[:i], func(d Card) bool { return d.ID == c.ID || d.View == c.View }):
			errs = append(errs, fmt.Errorf("tui: the card %s is declared twice", c.ID))
		case !identifier(c.ID, false) || !identifier(c.View, true):
			errs = append(errs, fmt.Errorf("tui: the card %q (view %q) needs a lower-camel ID and an upper-camel View", c.ID, c.View))
		case taken[c.ID]:
			errs = append(errs, fmt.Errorf("tui: the card %s takes an id main.qml already has", c.ID))
		case c.Back != "" && !slices.ContainsFunc(k.cards, func(d Card) bool { return d.ID == c.Back }):
			errs = append(errs, fmt.Errorf("tui: the card %s goes back to the card %s, which is not declared", c.ID, c.Back))
		}
	}
	for _, key := range k.keys {
		if n := len(slices.DeleteFunc([]string{key.Run, key.Opens, key.Cmd}, func(s string) bool { return s == "" })); n != 1 {
			errs = append(errs, fmt.Errorf("tui: %s %s runs %d of Run, Opens and Cmd: a key runs exactly one", placeName(key.Card), key.Seq, n))
		}
		switch {
		case key.Cmd == "":
		case strings.HasPrefix(key.Cmd, "lua.") || strings.HasPrefix(key.Cmd, "plugin."):
			errs = append(errs, fmt.Errorf("tui: %s %s runs %s: a key runs a built-in or a feature's command, never Lua's or a plugin's", placeName(key.Card), key.Seq, key.Cmd))
		case cat.lookup(key.Cmd) == nil:
			errs = append(errs, fmt.Errorf("tui: %s %s runs %s, which no command is", placeName(key.Card), key.Seq, key.Cmd))
		}
		if _, ok := k.card(key.Card); key.Card != "" && !ok {
			errs = append(errs, fmt.Errorf("tui: %s is on the card %s, which is not declared", key.Seq, key.Card))
		}
		switch {
		case key.Scope == "":
		case key.Card != "":
			errs = append(errs, fmt.Errorf("tui: %s on the card %s names the pane %s: a card's keys are the card's", key.Seq, key.Card, key.Scope))
		case !slices.ContainsFunc(keyScopes, func(s keyScope) bool { return s.ID == key.Scope }):
			errs = append(errs, fmt.Errorf("tui: %s is the pane %s's, which main.qml does not scope", key.Seq, key.Scope))
		}
		if key.Opens != "" {
			c, ok := k.card(key.Opens)
			switch {
			case !ok:
				errs = append(errs, fmt.Errorf("tui: %s opens the card %s, which is not declared", key.Seq, key.Opens))
			case key.Card != c.Back:
				errs = append(errs, fmt.Errorf("tui: %s on %s opens the card %s, whose Backspace goes back to %s", key.Seq, placeName(key.Card), c.ID, placeName(c.Back)))
			}
			opened[key.Opens] = true
		}
	}
	for _, c := range k.cards {
		if !opened[c.ID] {
			errs = append(errs, fmt.Errorf("tui: no key opens the card %s", c.ID))
		}
	}
	for _, place := range append([]string{""}, cardIDs(k.cards)...) {
		seen := map[string]bool{}
		if c, ok := k.card(place); ok {
			for _, s := range reserved {
				seen[s] = true
			}
			if c.Back != "" {
				seen["Backspace"] = true
			}
		}
		for _, key := range k.on(place) {
			for _, s := range append([]string{key.Seq}, key.Also...) {
				if seen[s] {
					errs = append(errs, fmt.Errorf("tui: %s %s is taken twice", placeName(place), s))
				}
				seen[s] = true
			}
		}
	}
	for _, scope := range keyScopes {
		seen := map[string]bool{}
		for _, key := range k.in(scope.ID) {
			for _, s := range append([]string{key.Seq}, key.Also...) {
				if seen[s] {
					errs = append(errs, fmt.Errorf("tui: the pane %s's %s is taken twice", scope.ID, s))
				}
				seen[s] = true
			}
		}
	}
	return errors.Join(errs...)
}

func cardIDs(cs []Card) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// identifier reports whether s is a QML identifier of one word, lower-camel ("agentsCard") or
// upper-camel ("AgentsCard").
func identifier(s string, upper bool) bool {
	if s == "" {
		return false
	}
	if first := s[0]; upper && !(first >= 'A' && first <= 'Z') || !upper && !(first >= 'a' && first <= 'z') {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// mainIDs are the ids main.qml declares outside its generated cards: a card may take none of them.
var mainIDs = func() map[string]bool {
	src, err := fs.ReadFile(qmlFiles, "main.qml")
	if err != nil {
		panic("tui: embedded main.qml: " + err.Error())
	}
	s := string(src)
	if a, b := strings.Index(s, cardsBegin), strings.Index(s, cardsEnd); a >= 0 && b > a {
		s = s[:a] + s[b:]
	}
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bid:\s*([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(s, -1) {
		ids[m[1]] = true
	}
	return ids
}()

// placeName is how a refusal names where a key is answered.
func placeName(card string) string {
	if card == "" {
		return "the Window's"
	}
	return "the card " + card + "'s"
}

// cardText is a card's body, its keys in the catalog's order. With groups, each key is under its
// heading, the headings in columns side by side, a blank line between two of a column: SPC's
// card holds too many keys for two a row to fit the screen once headed. With none, two commands a
// row, the second column aligned.
func cardText(groups [][]string, keys []Key) string {
	var listed []Key
	for _, k := range keys {
		if k.Does != "" {
			listed = append(listed, k)
		}
	}
	if groups == nil {
		cols := make([][]string, 2)
		for i, k := range listed {
			cols[i%2] = append(cols[i%2], k.Show+"  "+k.Does)
		}
		return sideBySide(cols)
	}
	headed := map[string]bool{}
	for _, g := range slices.Concat(groups...) {
		headed[g] = true
	}
	under := map[string][]string{}
	for _, k := range listed {
		g := k.Group
		if !headed[g] {
			g = groupMore
		}
		under[g] = append(under[g], k.Show+"  "+k.Does)
	}
	cols := make([][]string, len(groups))
	for i, col := range groups {
		if i == len(groups)-1 {
			col = append(slices.Clone(col), groupMore) // a key in no group of the card's is still listed
		}
		for _, g := range col {
			if len(under[g]) == 0 {
				continue
			}
			if len(cols[i]) > 0 {
				cols[i] = append(cols[i], "")
			}
			cols[i] = append(append(cols[i], g), under[g]...)
		}
	}
	return sideBySide(cols)
}

// sideBySide is cols as text, row by row, each column starting two cells after the widest line of
// the one before it. An empty column takes no room.
func sideBySide(cols [][]string) string {
	cols = slices.DeleteFunc(slices.Clone(cols), func(c []string) bool { return len(c) == 0 })
	rows := 0
	widths := make([]int, len(cols))
	for i, c := range cols {
		rows = max(rows, len(c))
		for _, line := range c {
			widths[i] = max(widths[i], utf8.RuneCountInString(line))
		}
	}
	lines := make([]string, rows)
	for r := range lines {
		var b strings.Builder
		for i, c := range cols {
			cell := ""
			if r < len(c) {
				cell = c[r]
			}
			b.WriteString(cell)
			if i < len(cols)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]+2-utf8.RuneCountInString(cell)))
			}
		}
		lines[r] = strings.TrimRight(b.String(), " ")
	}
	return strings.Join(lines, "\n")
}

// textSource is the App.* source a card's body is: App.leaderText for SPC's.
func textSource(card string) string { return "App." + card + "Text" }

// cardState is every card's body, as the document reads it.
func (k keyset) cardState() map[string]any {
	m := map[string]any{}
	for _, c := range k.cards {
		m[textSource(c.ID)] = cardText(c.Groups, k.on(c.ID))
	}
	return m
}

// helpCards is every card as help lists it, under its heading.
func (k keyset) helpCards() string {
	var parts []string
	for _, c := range k.cards {
		parts = append(parts, c.Help+"\n  "+strings.ReplaceAll(cardText(c.Groups, k.on(c.ID)), "\n", "\n  "))
	}
	return strings.Join(parts, "\n\n")
}

// shortcuts are the QML Shortcuts for keys, indented: each closes the card it is on first, when
// close names it; a key that opens a card opens it.
func shortcuts(keys []Key, indent, close string) string {
	var b strings.Builder
	for _, k := range keys {
		run := k.Run
		switch {
		case k.Opens != "":
			run = k.Opens + ".open()"
		case k.Cmd != "":
			run = "App.run(" + strconv.Quote(k.Cmd) + ")"
		}
		if close != "" {
			run = "{ " + close + ".close(); " + run + " }"
		}
		for _, s := range append([]string{k.Seq}, k.Also...) {
			fmt.Fprintf(&b, "%sShortcut { sequence: %q; onActivated: %s }\n", indent, s, run)
		}
	}
	return b.String()
}

// The generated blocks of main.qml.
const (
	keysBegin  = "    // ---- the Window's keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----\n"
	keysEnd    = "    // ---- end of the generated keys ----\n"
	cardsBegin = "    // ---- the cards of keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----\n"
	cardsEnd   = "    // ---- end of the generated cards ----\n"
)

// scopeMarkers are the lines around a pane's generated keys, inside its FocusScope.
func scopeMarkers(s keyScope) (begin, end string) {
	return s.Indent + "// ---- the " + s.ID + "'s keys: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) ----\n",
		s.Indent + "// ---- end of the " + s.ID + "'s keys ----\n"
}

// cardQML is views/<View>.qml for card c and its keys.
func cardQML(c Card, keys []Key) string {
	back := ""
	if c.Back != "" {
		back = fmt.Sprintf("    Shortcut { sequence: \"Backspace\"; onActivated: { %s.close(); %s.open() } }\n", c.ID, c.Back)
	}
	help := "a key runs its command · q or Esc closes"
	if c.Back != "" {
		help = "a key runs its command · Backspace goes back · q or Esc closes"
	}
	return `// ` + c.View + `.qml — a card of keys, as AutoDB's: every command here, by key. GENERATED from
// tui/keys.go (go test ./tui -run TestKeysAreGenerated -update): edit the catalog there, not this
// file. The rows are TEXT, not a list: a list takes the keyboard and eats letters that are keys
// here. With nothing focusable in it, every key reaches the dialog's Shortcuts, one a command, and
// each closes the card before its command runs, so what the command opens is not under it. Esc
// closes it.
Dialog {
    id: ` + c.ID + `
    closeOnQ: true
    title: ` + strconv.Quote(c.Title) + `
    dim: false
    helpText: ` + strconv.Quote(help) + `
    Text { text: ` + textSource(c.ID) + `; wrapMode: Tui.WordWrap } // WordWrap keeps its lines: NoWrap is one
` + back + shortcuts(keys, "    ", c.ID) + "}\n"
}

// withGenerated is main.qml with its generated blocks replaced by the catalog's.
func (k keyset) withGenerated(mainQML string) (string, error) {
	var decls strings.Builder
	for _, c := range k.cards {
		fmt.Fprintf(&decls, "    %s { id: %s }\n", c.View, c.ID)
	}
	out, err := replaceBlock(mainQML, keysBegin, keysEnd, shortcuts(k.on(""), "    ", ""))
	if err != nil {
		return "", err
	}
	for _, scope := range keyScopes {
		begin, end := scopeMarkers(scope)
		if out, err = replaceBlock(out, begin, end, shortcuts(k.in(scope.ID), scope.Indent, "")); err != nil {
			return "", err
		}
	}
	return replaceBlock(out, cardsBegin, cardsEnd, decls.String())
}

// replaceBlock is s with what lies between begin and end replaced by body.
func replaceBlock(s, begin, end, body string) (string, error) {
	a := strings.Index(s, begin)
	b := strings.Index(s, end)
	if a < 0 || b < a {
		return "", fmt.Errorf("tui: main.qml lacks the markers %q", strings.TrimSpace(begin))
	}
	return s[:a+len(begin)] + body + s[b:], nil
}
