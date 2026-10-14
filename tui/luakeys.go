package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"

	"github.com/yongjohnlee80/autodoc/script"
)

// LUA KEYS AND MENU ROWS (ADR 1792004705 §4.5–4.7): a script binds a command line to a key or a
// menu row.
//
//	autodoc.map{mode = "normal" | "visual", keys = "Q" | "<C-k>", command = "heading"}
//	autodoc.map{mode = "leader", keys = "t", command = "table 2 3", title = "a table"}  -- SPC u t
//	autodoc.menu{where = "editor" | "plugins", title = "&Word count", command = "wc"}
//
// A Normal or Visual chord reaches the page's editor through golib's chord hook, with nothing
// pending; a leader key is a letter of SPC u, a card built at run time as SPC p's is; a menu row
// is the right-click menu's (editorMenu) or the Plugins menu's. Each runs its command line as ':'
// does, its error a notification. They belong to the engine that made them: a reload drops them
// with it (bind), and the scripts make them again as they load. A gated script needs commands for
// any of them to run, as for autodoc.run: what they run is any command. Its keys and rows register
// whatever it was granted, and ask for the grant when they run, so an Allow given after it loaded
// works at once.

// luaKey is a letter of SPC u.
type luaKey struct {
	letter, title, line string
	by                  luaBinder
}

// luaMenu is a menu row a script added.
type luaMenu struct {
	where, title, line string
	by                 luaBinder
}

// luaBinder is who bound a key or a row: a gated script's needs commands when it runs; an
// ungated script's (zero) does not.
type luaBinder struct {
	script string
	grants script.Grants
}

// allowed reports whether what b bound may run now, and says why not when it may not.
func (b luaBinder) allowed(s *scripting) bool {
	if b.grants == nil || b.grants.Granted(b.script, "commands") {
		return true
	}
	s.c.Notify(b.script + ": its keys and menu rows need commands: not granted")
	return false
}

// binder is the script calling map or menu now.
func (s *scripting) binder() luaBinder {
	if name, g, ok := s.eng.Caller(); ok {
		return luaBinder{name, g}
	}
	return luaBinder{}
}

// luaChord is a page key: its command line, and who bound it.
type luaChord struct {
	line string
	by   luaBinder
}

// luaUI is one engine's keys and rows.
type luaUI struct {
	chords map[widget.KeyChord]luaChord
	leader []luaKey
	menus  []luaMenu
}

// luaLetters are SPC u's: q closes the card.
const luaLetters = "abcdefghijklmnoprstuvwxyz0123456789"

// mapUI starts an empty set of keys and rows for a new engine, and binds map and menu.
func (s *scripting) mapUI(eng interface {
	SetFunction(string, lua.LGFunction)
}) {
	s.ui = luaUI{chords: map[widget.KeyChord]luaChord{}}
	s.refreshLuaUI()
	eng.SetFunction("map", s.luaMap)
	eng.SetFunction("menu", s.luaMenuFn)
}

// luaMap is autodoc.map{mode, keys, command, title}.
func (s *scripting) luaMap(L *lua.LState) int {
	spec := L.CheckTable(1)
	mode, keys := lua.LVAsString(spec.RawGetString("mode")), lua.LVAsString(spec.RawGetString("keys"))
	line, title := lua.LVAsString(spec.RawGetString("command")), lua.LVAsString(spec.RawGetString("title"))
	if line == "" {
		L.ArgError(1, "map needs a command: the command line it runs")
	}
	switch mode {
	case "normal", "visual":
		kc, err := chordOf(mode, keys)
		if err != nil {
			L.ArgError(1, err.Error())
		}
		s.ui.chords[kc] = luaChord{line, s.binder()}
	case "leader":
		if utf8.RuneCountInString(keys) != 1 || !strings.Contains(luaLetters, keys) {
			L.ArgError(1, fmt.Sprintf("a leader key is one of %s (q closes the card), not %q", luaLetters, keys))
		}
		if title == "" {
			title = line
		}
		k := luaKey{keys, title, line, s.binder()}
		replaced := false
		for i := range s.ui.leader {
			if s.ui.leader[i].letter == keys {
				s.ui.leader[i], replaced = k, true
			}
		}
		if !replaced {
			s.ui.leader = append(s.ui.leader, k)
		}
		s.refreshLuaUI()
	default:
		L.ArgError(1, fmt.Sprintf("map's mode is normal, visual or leader, not %q", mode))
	}
	return 0
}

// chordOf reads a map's keys: one character ("Q"), or a control chord ("<C-k>").
func chordOf(mode, keys string) (widget.KeyChord, error) {
	m := widget.ModeNormal
	if mode == "visual" {
		m = widget.ModeVisual
	}
	if inner, ok := strings.CutPrefix(keys, "<C-"); ok && strings.HasSuffix(inner, ">") {
		inner = strings.TrimSuffix(inner, ">")
		if r, n := utf8.DecodeRuneInString(inner); n == len(inner) && n > 0 {
			return widget.KeyChord{Mode: m, Code: r, Ctrl: true}, nil
		}
	}
	if r, n := utf8.DecodeRuneInString(keys); n == len(keys) && n > 0 && r != ' ' {
		return widget.KeyChord{Mode: m, Code: r}, nil
	}
	return widget.KeyChord{}, fmt.Errorf("a %s key is one character, \"Q\", or a control chord, \"<C-k>\", not %q; a sequence is a leader key", mode, keys)
}

// luaMenuFn is autodoc.menu{where, title, command}.
func (s *scripting) luaMenuFn(L *lua.LState) int {
	spec := L.CheckTable(1)
	where := lua.LVAsString(spec.RawGetString("where"))
	title, line := lua.LVAsString(spec.RawGetString("title")), lua.LVAsString(spec.RawGetString("command"))
	if where != "editor" && where != "plugins" {
		L.ArgError(1, fmt.Sprintf("menu's where is editor or plugins, not %q", where))
	}
	if title == "" || line == "" {
		L.ArgError(1, "menu needs a title and a command")
	}
	s.ui.menus = append(s.ui.menus, luaMenu{where, title, line, s.binder()})
	s.refreshLuaUI()
	return 0
}

// luaKeysText is SPC u's card as the keys stand.
func (s *scripting) luaKeysText() string {
	if len(s.ui.leader) == 0 {
		return "no Lua keys yet: autodoc.map{mode = \"leader\", …} adds one"
	}
	var lines []string
	for _, k := range s.ui.leader {
		lines = append(lines, k.letter+"  "+k.title)
	}
	return strings.Join(lines, "\n")
}

// refreshLuaUI shows SPC u's letters and the Plugins menu's Lua rows as they are now.
func (s *scripting) refreshLuaUI() {
	var rows []tuidecl.Row
	for i, m := range s.ui.menus {
		if m.where == "plugins" {
			rows = append(rows, tuidecl.Row{"label": m.title, "target": strconv.Itoa(i)})
		}
	}
	s.luaMenuRows().Reset(rows)
	// before the program runs, State's first value is the text as it stands then
	if a, ok := s.c.(interface{ Attached() bool }); ok && a.Attached() {
		s.c.Set("App.luaKeysText", s.luaKeysText())
	}
}

func (s *scripting) luaMenuRows() *tuidecl.ListModel {
	if s.menuRows == nil {
		s.menuRows = tuidecl.NewListModel("label", "target")
	}
	return s.menuRows
}

// luaKey is SPC u's letter, chosen: the card closes, then its command runs.
func (s *scripting) luaKey(letter string) {
	for _, k := range s.ui.leader {
		if k.letter == letter {
			s.c.Close("luaKeys")
			if k.by.allowed(s) {
				s.runLuaLine(k.line)
			}
			return
		}
	}
}

// luaMenu is a Plugins-menu row of a script's, chosen.
func (s *scripting) luaMenu(target string) {
	if i, err := strconv.Atoi(target); err == nil && i >= 0 && i < len(s.ui.menus) && s.ui.menus[i].by.allowed(s) {
		s.runLuaLine(s.ui.menus[i].line)
	}
}

// chord is the page's chord hook: a script's Normal or Visual chord runs its command line.
func (s *scripting) chord(kc widget.KeyChord) bool {
	ch, ok := s.ui.chords[kc]
	if !ok {
		return false
	}
	if ch.by.allowed(s) {
		s.runLuaLine(ch.line)
	}
	return true // the script's key all the same: it does nothing else while refused
}

// runLuaLine runs a key's or a row's command line as ':' does; its error is a notification.
func (s *scripting) runLuaLine(line string) {
	if s.line == nil {
		return
	}
	if err := s.line(line); err != nil {
		s.c.Notify(line + ": " + err.Error())
	}
}

// editorRows are the right-click menu's Lua rows, after a separator; none without one.
func (s *scripting) editorRows() []widget.MenuItemModel {
	var rows []widget.MenuItemModel
	for i, m := range s.ui.menus {
		if m.where != "editor" {
			continue
		}
		line, by := m.line, m.by
		id := "lua.menu." + strconv.Itoa(i)
		rows = append(rows, menuCommand(widget.ItemID(id), m.title, widget.CoreMenuAction{ID: tuicore.ActionID(id),
			Run: func(*widget.EditorCore) {
				if by.allowed(s) {
					s.runLuaLine(line)
				}
			}}))
	}
	if len(rows) == 0 {
		return nil
	}
	return append([]widget.MenuItemModel{widget.NewSeparator("lua.sep")}, rows...)
}
