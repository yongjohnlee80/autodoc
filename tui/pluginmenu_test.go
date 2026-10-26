package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// The cells of ADR 1792991184 §4.5-4.6: a plugin's [[menu]] rows, and host.menu (protocol 11). The
// plugins are the test binary in mode "rawcalls": ping says pong, menuwith sends host.menu.

const menuCommands = "[[commands]]\nid = \"ping\"\ntitle = \"Ping\"\n[[commands]]\nid = \"menuwith\"\ntitle = \"Menu\"\n"

// rowAt is the added row labelled label ("Sub>Row" in a submenu), as the menu opens for ctx.
func (r *running) rowAt(ctx menuCtx, label string) (widget.MenuItemModel, bool) {
	for _, it := range r.menuAt(ctx) {
		for _, ch := range it.Children {
			if it.Label+">"+ch.Label == label {
				return ch, true
			}
		}
		if it.Label == label {
			return it, true
		}
	}
	return widget.MenuItemModel{}, false
}

// A plugin's [[menu]] row shows under its title for its files, and runs its command, starting the
// plugin. While it runs, host.menu sets the row's title and disables it; a row its manifest doesn't
// declare, and a protocol-10 plugin's host.menu, are refused. Stopped, its rows are as declared. A
// script's row under the plugin's name joins its submenu; :lua-reload keeps the plugin's rows, and
// removing the plugin drops them.
func TestAPluginsMenuRows(t *testing.T) {
	dir := t.TempDir()
	installPluginAt(t, dir, "db", "service", "rawcalls", menuCommands+
		"[[menu]]\ncommand = \"ping\"\nwhere = \"editor\"\nfiles = [\"*.sql\"]\ntitle = \"&Execute\"\n", 11)
	installPluginAt(t, dir, "old", "service", "rawcalls", menuCommands, 10)
	r, logs := pluginTUIWith(t, dir, nil)
	sql := menuCtx{path: "q.sql"}

	if got := labelsOf(r.menuAt(menuCtx{path: "a.md"})); len(got) != 0 {
		t.Fatalf("a.md shows %q; the row is *.sql's", got)
	}
	row, ok := r.rowAt(sql, "Db>Execute")
	if !ok || row.Hotkey != 'e' || !row.Enabled {
		t.Fatalf("q.sql's row: %+v %v; the menu shows %q", row, ok, labelsOf(r.menuAt(sql)))
	}
	onLoop(r, func() bool { row.Action.(widget.CoreMenuAction).Run(r.h.core); return true })
	waitSaid(t, logs, "db", "pong")

	r.pluginCommand("db", "menuwith", "ping", "Execute on main", "0")
	r.s.WaitFor(t, "the pushed title", func(string) bool { _, ok := r.rowAt(sql, "Db>Execute on main"); return ok })
	if row, _ := r.rowAt(sql, "Db>Execute on main"); row.Enabled {
		t.Fatal("enabled = false left the row enabled")
	}
	// the plugins read again, their manifests as they were: the live run's title and state stay
	onLoop(r, func() bool { r.h.plugins.loadPlugins(); return true })
	if row, ok := r.rowAt(sql, "Db>Execute on main"); !ok || row.Enabled {
		t.Fatalf("after the plugins were read again: %+v %v; want the pushed title, disabled", row, ok)
	}
	// a row its manifest now declares otherwise is that declaration, as written and enabled
	manifest := filepath.Join(dir, "db", "plugin.toml")
	declared, _ := os.ReadFile(manifest)
	rewrite := func(b []byte) {
		if err := os.WriteFile(manifest, b, 0o644); err != nil {
			t.Fatal(err)
		}
		onLoop(r, func() bool { r.h.plugins.loadPlugins(); return true })
	}
	rewrite(bytes.Replace(declared, []byte(`title = "&Execute"`), []byte(`title = "&Run it"`), 1))
	if row, ok := r.rowAt(sql, "Db>Run it"); !ok || !row.Enabled {
		t.Fatalf("after its declaration changed: %+v %v; the menu shows %q", row, ok, labelsOf(r.menuAt(sql)))
	}
	rewrite(declared)
	r.pluginCommand("db", "menuwith", "ping", "Execute on main", "0")
	r.s.WaitFor(t, "the title pushed again", func(string) bool { row, ok := r.rowAt(sql, "Db>Execute on main"); return ok && !row.Enabled })
	r.pluginCommand("db", "menuwith", "nope", "X", "")
	r.pluginCommand("old", "menuwith", "ping", "Old", "")
	waitLogged(t, logs, "db", `host.menu refused: no [[menu]] row "nope"`)
	waitLogged(t, logs, "old", "host.menu refused: the manifest's protocol 10 is before 11")

	r.turnOff("db")
	r.s.WaitFor(t, "the declared title again", func(string) bool { row, ok := r.rowAt(sql, "Db>Execute"); return ok && row.Enabled })

	r.luaLoad(t, `autodoc.menu{ where = "editor", menu = "Db", title = "From Lua", command = "x" }`)
	if got := labelsOf(r.menuAt(sql)); !same(got, []string{"Db>Execute", "Db>From Lua"}) {
		t.Fatalf("a script's row under the plugin's name: %q", got)
	}
	r.mustRun(t, "lua-reload")
	if got := labelsOf(r.menuAt(sql)); !same(got, []string{"Db>Execute"}) {
		t.Fatalf("after :lua-reload: %q; want the plugin's row alone", got)
	}
	r.remove(t, dir, "db")
	if got := labelsOf(r.menuAt(sql)); len(got) != 0 {
		t.Fatalf("after removing the plugin: %q", got)
	}
}
