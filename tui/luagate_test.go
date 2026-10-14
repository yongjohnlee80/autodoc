package tui

import (
	"context"
	"strings"
	"testing"
)

// gatedLua is a package-like script: it asks for reading the workspace, and its :pkg tries a read
// and a write, writing what each answered into the page.
const gatedLua = `
autodoc.requires{"workspace.read"}
autodoc.command{ name = "pkg", run = function(ed)
  local ws = autodoc.current_workspace()
  local ok, err = autodoc.doc.read(ws, "a.md", function(res, e)
    ed:insert("[read " .. tostring(res and res.content) .. "]")
  end)
  local wok, werr = autodoc.call("doc.write", {ws, "a.md", "gone", ""})
  -- one insert: the answers in the order made, whatever Normal mode does with the cursor
  ed:insert("[" .. tostring(ok) .. " " .. tostring(err) .. "][" .. tostring(wok) .. " " .. tostring(werr) .. "]")
end }`

// TestAGatedScriptAsksAndIsHeld: a script loaded gated is held to what it was granted. Before the
// card is answered its read is refused as a value; Allow grants what it asked for, kept in the
// lua.grants preference, and its read works at once with no reload; a write it never asked for
// stays refused, and the file is unchanged.
func TestAGatedScriptAsksAndIsHeld(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	if err := onLoop(r, func() error { return r.h.loadGated("pkg", "pkg.lua", []byte(gatedLua)) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "let the Lua script pkg use workspace.read?")

	r.page("", 1, 1)
	r.mustRun(t, "pkg")
	r.wantText(t, ":pkg ungranted",
		"[nil needs workspace.read: not granted][nil needs workspace.write: not granted]")

	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	r.s.WaitFor(t, "the grant kept", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.luaGrants["pkg"]["workspace.read"] })
	})
	r.s.WaitFor(t, "lua.grants in the store", func(string) bool {
		all, err := d.db.Preferences(context.Background())
		return err == nil && strings.Contains(all[prefLuaGrants], `"pkg":{"workspace.read":true}`)
	})

	r.page("", 1, 1)
	r.mustRun(t, "pkg")
	r.s.WaitFor(t, "the read's answer", func(string) bool { return strings.Contains(r.editorText(), "[read kestrel") })
	if got := r.editorText(); !strings.Contains(got, "[nil nil]") || !strings.Contains(got, "[nil needs workspace.write: not granted") {
		t.Fatalf(":pkg granted: %q", got)
	}
	if got := d.read(t, "kb", "a.md"); got != "kestrel\n" {
		t.Fatalf("a.md is %q: the refused write wrote", got)
	}
}
