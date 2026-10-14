package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// keysLua binds :mark to a chord, a control chord, a leader letter and both menus.
const keysLua = `
autodoc.command{ name = "mark", run = function(ed, args, line) ed:insert("<" .. line .. ">") end }
autodoc.map{ mode = "normal", keys = "Q", command = "mark q" }
autodoc.map{ mode = "normal", keys = "<C-y>", command = "mark cy" }
autodoc.map{ mode = "visual", keys = "Q", command = "mark vq" }
autodoc.map{ mode = "leader", keys = "t", command = "mark lt", title = "mark it" }
autodoc.menu{ where = "plugins", title = "Mark", command = "mark pm" }
autodoc.menu{ where = "editor", title = "&Mark here", command = "mark em" }`

// TestLuaKeysAndMenusRunTheirCommands: a script's Normal chord and control chord reach the page
// through golib's hook (and a Visual one only in Visual mode); SPC u t, pressed as keys, runs the
// leader key; the Plugins menu's row and the right-click menu's run theirs. Bad specs are the
// script's errors; a gated script needs commands to bind any; :lua-reload drops them all.
func TestLuaKeysAndMenusRunTheirCommands(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	r.luaLoad(t, keysLua)
	press := func(k tuicore.KeyEvent) bool { return onLoop(r, func() bool { return r.h.core.HandleKey(k) }) }

	r.page("", 1, 1)
	if !press(tuicore.KeyEvent{Code: 'Q', Text: "Q"}) || !press(tuicore.KeyEvent{Code: 'y', Mods: tuicore.ModCtrl}) {
		t.Fatal("a mapped chord was not taken")
	}
	if got := r.editorText(); !strings.Contains(got, "<q>") || !strings.Contains(got, "<cy>") || strings.Contains(got, "<vq>") {
		t.Fatalf("the Normal chords: %q", got)
	}

	// SPC u t, as keys: the leader, its card, the letter
	r.page("", 1, 1)
	r.keys(t, key(' '))
	r.keys(t, key('u'))
	r.s.WaitForText(t, "SPC u — your Lua keys")
	r.s.WaitForText(t, "t  mark it")
	r.keys(t, key('t'))
	r.s.WaitFor(t, "SPC u t ran", func(string) bool { return r.editorText() == "<lt>" })

	r.page("", 1, 1)
	rows := onLoop(r, func() int { return r.h.luaFeature().luaMenuRows().Len() })
	if rows != 1 {
		t.Fatalf("the Plugins menu has %d Lua rows", rows)
	}
	onLoop(r, func() bool { r.h.luaFeature().luaMenu("0"); return true }) // the Plugins row: the first menu{}
	var row widget.MenuItemModel
	onLoop(r, func() bool {
		for _, it := range r.h.editorMenu(r.h.core) {
			if it.Label == "Mark here" {
				row = it
			}
		}
		return true
	})
	if row.Label == "" {
		t.Fatal("the right-click menu has no Lua row")
	}
	if got := r.editorText(); got != "<pm>" {
		t.Fatalf("the Plugins menu's row ran %q", got)
	}

	// bad specs refuse the script
	for src, want := range map[string]string{
		`autodoc.map{ mode = "normal", keys = "gx", command = "mark" }`:   "a normal key is one character",
		`autodoc.map{ mode = "leader", keys = "q", command = "mark" }`:    "q closes the card",
		`autodoc.map{ mode = "insert", keys = "x", command = "mark" }`:    "mode is normal, visual or leader",
		`autodoc.menu{ where = "status", title = "x", command = "mark" }`: "where is editor or plugins",
		`autodoc.map{ mode = "normal", keys = "Z" }`:                      "map needs a command",
	} {
		if err := onLoop(r, func() error { return r.h.scripts.Load("bad.lua", []byte(src)) }); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v; want %q", src, err, want)
		}
	}

	// a gated script without commands binds nothing
	r.page("", 1, 1)
	if err := onLoop(r, func() error {
		return r.h.loadGated("pkg", "pkg.lua", []byte(`local ok, err = autodoc.map{ mode = "normal", keys = "W", command = "mark w" }
autodoc.command{ name = "said", run = function(ed) ed:insert(tostring(ok) .. " " .. tostring(err)) end }`))
	}); err != nil {
		t.Fatal(err)
	}
	r.mustRun(t, "said")
	r.wantText(t, "a gated map", "nil needs commands: not granted")
	if onLoop(r, func() bool {
		_, ok := r.h.luaFeature().ui.chords[widget.KeyChord{Mode: widget.ModeNormal, Code: 'W'}]
		return ok
	}) {
		t.Fatal("a gated script without commands bound W")
	}

	// a reload drops the test's script, and every key and row with it
	r.mustRun(t, "lua-reload")
	ui := onLoop(r, func() luaUI { return r.h.luaFeature().ui })
	if len(ui.chords) != 0 || len(ui.leader) != 0 || len(ui.menus) != 0 {
		t.Fatalf("after the reload: %+v", ui)
	}
	r.page("abc", 1, 1)
	press(tuicore.KeyEvent{Code: 'Q', Text: "Q"})
	if got := r.editorText(); strings.Contains(got, "<q>") {
		t.Fatalf("Q still ran :mark after the reload: %q", got)
	}
}
