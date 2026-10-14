package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLuaReloadReadsTheScriptsAgain: :lua-reload loads every script again on a new engine. An
// edited file's command is the new one, a removed file's is gone and the name it took is back
// with its owner, AutoDoc's own scripts are there still, and a callback of the old engine that
// comes after is dropped, neither run nor said as a failure. A script cannot reload from inside
// itself. Saving one of the user's scripts in AutoDoc reloads too.
func TestLuaReloadReadsTheScriptsAgain(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.lua", `autodoc.command{ name = "greet", run = function(ed) ed:insert("v1") end }
autodoc.command{ name = "slow", run = function(ed)
  autodoc.call("workspace.list", {}, function() ed:insert("stale") end)
end }
autodoc.command{ name = "self", run = function(ed)
  local ok, err = autodoc.run("scripts.reload")
  ed:insert("[" .. tostring(ok) .. " " .. tostring(err) .. "]")
end }`)
	write("b.lua", `autodoc.command{ name = "find", title = "mine", run = function(ed) ed:insert("myfind") end }`)
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.waitNamespaces(t)
	owner := func(name string) string { return onLoop(r, func() string { return r.h.cat.names[name] }) }

	r.page("", 1, 1)
	r.mustRun(t, "greet")
	r.wantText(t, ":greet", "v1")
	if got := owner("find"); got != "lua.find" {
		t.Fatalf(":find is %s's before the reload", got)
	}

	// the old engine's call is on its way when the scripts reload
	write("a.lua", `autodoc.command{ name = "greet", run = function(ed) ed:insert("v2") end }
autodoc.command{ name = "self", run = function(ed)
  local ok, err = autodoc.run("scripts.reload")
  ed:insert("[" .. tostring(ok) .. " " .. tostring(err) .. "]")
end }
autodoc.command{ name = "fresh", run = function(ed)
  autodoc.call("workspace.list", {}, function() ed:insert("fresh") end)
end }`)
	if err := os.Remove(filepath.Join(dir, "b.lua")); err != nil {
		t.Fatal(err)
	}
	r.page("", 1, 1)
	r.clearNotices()
	if err := onLoop(r, func() error {
		if err := r.h.core.RunCommand("slow"); err != nil {
			return err
		}
		return r.h.core.RunCommand("lua-reload")
	}); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "reloaded the Lua scripts")
	r.mustRun(t, "fresh") // the new engine's call: its answer comes after the old one's
	r.s.WaitFor(t, "the new engine's callback", func(string) bool { return strings.Contains(r.editorText(), "fresh") })
	time.Sleep(300 * time.Millisecond) // the old answer, had it been run, would be in by now
	if got := r.editorText(); got != "fresh" {
		t.Fatalf("after the reload the page is %q: the old engine's callback ran", got)
	}
	if onLoop(r, func() bool {
		for _, n := range r.h.noticeLog.items {
			if strings.Contains(n.text, "a Lua callback failed") {
				return true
			}
		}
		return false
	}) {
		t.Fatal("the old engine's callback was said as a failure")
	}

	r.page("", 1, 1)
	r.mustRun(t, "greet")
	r.wantText(t, ":greet after the reload", "v2")
	if got := owner("find"); got != "find.term" {
		t.Fatalf(":find is %s's after b.lua went; want AutoDoc's find.term", got)
	}
	if got := owner("mermaid"); got != "lua.mermaid" {
		t.Fatalf(":mermaid is %q's after the reload", got)
	}
	r.page("", 1, 1)
	r.mustRun(t, "self")
	r.wantText(t, ":self", "[false lua-reload: not from inside a script]")

	// saving the user's script in AutoDoc reloads it
	path := filepath.Join(dir, "a.lua")
	r.h.p.Post(func() { r.h.setDirty(false); r.h.openOutside(path) }) // the draft's edits are the test's, not to keep
	r.s.WaitFor(t, "a.lua open", func(string) bool { n := r.file(); return n.open && n.path == path })
	r.clearNotices()
	src, _ := os.ReadFile(path)
	r.page(strings.Replace(string(src), `"v2"`, `"v3"`, 1), 1, 1)
	r.mustRun(t, "w")
	r.waitNotice(t, "reloaded the Lua scripts")
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"v3"`) {
		t.Fatalf("a.lua was not saved: %s", b)
	}
	if err := onLoop(r, func() error {
		r.h.core.SetValue("")
		r.h.setDirty(false)
		return r.h.core.RunCommand("greet")
	}); err != nil {
		t.Fatal(err)
	}
	r.wantText(t, ":greet after saving a.lua", "v3")
}

// TestSavingAScriptInAWorkspaceReloads: the config's lua/ may lie inside a workspace, so a script
// saved there is a workspace path: it reloads too, once resolved on disk. Another file of the
// workspace, or a script in another folder, does not.
func TestSavingAScriptInAWorkspaceReloads(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	root := onLoop(r, func() string { return r.h.rootOf("kb") })
	if root == "" {
		t.Fatal("the workspace's root is not listed")
	}
	onLoop(r, func() bool { r.h.luaDir = filepath.Join(root, "config", "lua"); return true })
	for _, p := range []string{"a.md", "config/lua/x.txt", "config/other/x.lua", "config/lua/deeper/x.lua"} {
		r.clearNotices()
		onLoop(r, func() bool { r.h.reloadOnSave("kb", p); return true })
		if onLoop(r, func() bool { return len(r.h.noticeLog.items) > 0 }) {
			t.Fatalf("saving %s reloaded", p)
		}
	}
	onLoop(r, func() bool { r.h.reloadOnSave("kb", "config/lua/x.lua"); return true })
	r.waitNotice(t, "reloaded the Lua scripts")
}
