package tui

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

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
//	autodoc.menu{where = "editor" | "plugins", title = "&Word count", command = "wc"}  -- see menurows.go
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
	s.c.Notify(s.c.Trf("autodoc.notice.luakeys.its_keys_and_menu_rows_need_commands_not_granted", b.script))
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
}

// luaLetters are SPC u's: q closes the card.
const luaLetters = "abcdefghijklmnoprstuvwxyz0123456789"

// mapUI starts an empty set of keys and rows for a new engine, and binds map and menu.
func (s *scripting) mapUI(eng interface {
	SetFunction(string, lua.LGFunction)
}) {
	s.ui = luaUI{chords: map[widget.KeyChord]luaChord{}}
	s.rows.drop(func(r *addedRow) bool { return r.source == fromLua }) // the old engine's; a plugin's [[menu]] stays
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

// luaMenuFields are the fields autodoc.menu takes: any other is refused.
var luaMenuFields = map[string]bool{"where": true, "id": true, "menu": true, "files": true, "when": true, "title": true, "command": true}

// luaMenuFn is autodoc.menu{where, id, menu, files, when, title, command}: a row of the editor's
// right-click menu or of the Plugins menu (menurows.go). It answers a handle whose remove drops
// the row; the same id from the same script again replaces the row in its place.
func (s *scripting) luaMenuFn(L *lua.LState) int {
	spec := L.CheckTable(1)
	spec.ForEach(func(k, _ lua.LValue) {
		if ks, ok := k.(lua.LString); !ok || !luaMenuFields[string(ks)] {
			L.ArgError(1, fmt.Sprintf("menu has no field %s", lua.LVAsString(k)))
		}
	})
	where := lua.LVAsString(spec.RawGetString("where"))
	if where != "editor" && where != "plugins" {
		L.ArgError(1, fmt.Sprintf("menu's where is editor or plugins, not %q", where))
	}
	r := &addedRow{source: fromLua, where: where, id: lua.LVAsString(spec.RawGetString("id")), menu: lua.LVAsString(spec.RawGetString("menu"))}
	line := lua.LVAsString(spec.RawGetString("command"))
	switch t := spec.RawGetString("title").(type) {
	case lua.LString:
		r.title = string(t)
	case *lua.LFunction:
		r.titleFn = t
	case *lua.LNilType:
	default:
		L.ArgError(1, "menu's title is a string or a function")
	}
	if (r.title == "" && r.titleFn == nil) || line == "" {
		L.ArgError(1, "menu needs a title and a command")
	}
	switch w := spec.RawGetString("when").(type) {
	case *lua.LFunction:
		r.when = w
	case *lua.LNilType:
	default:
		L.ArgError(1, "menu's when is a function")
	}
	switch f := spec.RawGetString("files").(type) {
	case *lua.LTable:
		for i := 1; i <= f.Len(); i++ {
			g, ok := f.RawGetInt(i).(lua.LString)
			if _, err := path.Match(string(g), ""); !ok || err != nil {
				L.ArgError(1, fmt.Sprintf("menu's files are globs: %s isn't one", lua.LVAsString(f.RawGetInt(i))))
			}
			r.files = append(r.files, string(g))
		}
	case *lua.LNilType:
	default:
		L.ArgError(1, "menu's files is a list of globs")
	}
	if where == "plugins" && (r.files != nil || r.when != nil || r.titleFn != nil) {
		L.ArgError(1, "a Plugins-menu row takes no files, when or title function: that menu isn't built as it opens")
	}
	by := s.binder()
	r.owner = by.script
	r.run = func() {
		if by.allowed(s) {
			s.runLuaLine(line)
		}
	}
	s.rows.add(r)
	s.refreshLuaUI()
	L.Push(s.rowHandle(L, r))
	return 1
}

// rowHandle is autodoc.menu's answer: row:remove() drops the row it added, and nothing else.
func (s *scripting) rowHandle(L *lua.LState, r *addedRow) lua.LValue {
	ud := L.NewUserData()
	ud.Value = r
	mt := L.NewTypeMetatable("autodoc.menu.row")
	if mt.RawGetString("__index") == lua.LNil {
		methods := L.NewTable()
		L.SetField(methods, "remove", L.NewFunction(func(L *lua.LState) int {
			if r, ok := L.CheckUserData(1).Value.(*addedRow); ok && s.rows.remove(r) {
				s.refreshLuaUI()
			}
			return 0
		}))
		L.SetField(mt, "__index", methods)
	}
	L.SetMetatable(ud, mt)
	return ud
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
	s.luaMenuRows().Reset(s.rows.pluginsMenuRows(s))
	// before the program runs, State's first value is the text as it stands then
	if a, ok := s.c.(interface{ Attached() bool }); ok && a.Attached() {
		s.c.Set("App.luaKeysText", s.luaKeysText())
	}
}

func (s *scripting) luaMenuRows() *tuidecl.ListModel {
	if s.menuRows == nil {
		s.menuRows = tuidecl.NewListModel("kind", "label", "target", "enabled", "rows")
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

// luaMenu is a Plugins-menu row of a script's, chosen: target is its serial.
func (s *scripting) luaMenu(target string) {
	if n, err := strconv.Atoi(target); err == nil {
		if r := s.rows.bySerial(n); r != nil && r.where == "plugins" {
			r.run()
		}
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
