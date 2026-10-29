package tui

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// A gated script's work is bounded however it runs (ADR 1792004705 §4.2a.2–2c): its user-only
// entries refused, the rights and commands among the preferences the user's, its opens and saves
// held to the workspace, its keys and rows run as itself long after it bound them. The daemon these
// cells run against serves its workspaces from memory with no guard: every bounded file.* is
// refused there, so a refusal here is the TUI's half (the bounds sent, the path entries' checks);
// the daemon's own enforcement is app/bounds_test.go's.

// grantedLua is a package granted everything but its rights: each command tries one thing and
// writes what it answered into the page.
const grantedLua = `
local function say(ed, ok, err) ed:insert("[" .. tostring(ok) .. " " .. tostring(err) .. "]") end
autodoc.command{ name = "shellrun", run = function(ed) say(ed, autodoc.run("scripts.shell", "autodoc.notify('PROBE-GATED')")) end }
autodoc.command{ name = "shelled", run = function(ed) say(ed, pcall(function() return ed:run("lua autodoc.notify('PROBE-GATED')") end)) end }
autodoc.command{ name = "launch", run = function(ed) say(ed, autodoc.run("file.open_default_app")) end }
autodoc.command{ name = "kbapply", run = function(ed) say(ed, autodoc.run("kbtype.apply", "https://example.invalid/kb.git")) end }
autodoc.command{ name = "wrapit", run = function(ed) say(ed, autodoc.run("view.wrap")) end }
autodoc.command{ name = "rights", run = function(ed)
  autodoc.call("preference.set", {"lua.grants", "{}"}, function(_, e) ed:insert("[grants " .. tostring(e) .. "]") end)
  autodoc.preference.set("tui.agent.profiles", "[]", function(_, e) ed:insert("[profiles " .. tostring(e) .. "]") end)
  autodoc.call("preference.set", {"tui.theme", "light"}, function(_, e) ed:insert("[theme " .. tostring(e) .. "]") end)
end }
autodoc.command{ name = "openit", run = function(ed) say(ed, autodoc.run("file.open_path", PROBE)) end }
autodoc.command{ name = "openline", run = function(ed) say(ed, pcall(function() return ed:run("e " .. PROBE) end)) end }
autodoc.command{ name = "saveit", run = function(ed) ed:insert("EVIL"); say(ed, autodoc.run("file.save")) end }
autodoc.command{ name = "follow", run = function(ed) say(ed, autodoc.run("file.follow_link")) end }
autodoc.command{ name = "bind", run = function(ed)
  autodoc.map{ mode = "leader", keys = "z", command = "w", title = "save" }
  autodoc.menu{ where = "plugins", title = "save it", command = "w" }
  say(ed, true, "bound")
end }`

// gatedRig is a TUI attached to a daemon serving kb, with the package pkg granted every capability
// it uses and grantedLua loaded gated; probe is a file outside every workspace.
type gatedRig struct {
	*running
	d     *daemon
	probe string
	mu    sync.Mutex
	urls  []string // what reached the browser
}

func newGatedRig(t *testing.T) *gatedRig {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(probe, []byte("orig\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prefs := maps.Clone(testPrefs)
	prefs[prefLuaGrants] = `{"pkg":{"commands":true,"settings":true,"workspace.read":true,"workspace.write":true}}`
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "kestrel\n"}}, daemonOpts{prefs: prefs})
	g := &gatedRig{running: attached(t, d), d: d, probe: probe}
	g.waitNamespaces(t)
	onLoop(g.running, func() bool {
		g.h.browser = func(_ context.Context, u string) error {
			g.mu.Lock()
			g.urls = append(g.urls, u)
			g.mu.Unlock()
			return nil
		}
		return true
	})
	src := "PROBE = " + strconv.Quote(probe) + "\n" + grantedLua
	if err := onLoop(g.running, func() error { return g.h.loadGated("pkg", "pkg.lua", []byte(src)) }); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *gatedRig) browsed() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.urls...)
}

// saved runs act, a save of the shown buffer, and waits until that save is settled, refused or
// not: the file is read after, never while the save is in flight.
func (g *gatedRig) saved(t *testing.T, act func()) {
	t.Helper()
	before := onLoop(g.running, func() uint64 { return g.h.active.saves })
	act()
	g.waitWithin(t, "the save settled", daemonWait, func() bool {
		return onLoop(g.running, func() bool { return g.h.active.saves > before && !g.h.active.saving })
	})
}

func (g *gatedRig) probeIs(t *testing.T, want string) {
	t.Helper()
	if b, _ := os.ReadFile(g.probe); string(b) != want {
		t.Errorf("the probe outside every workspace holds %q, want %q", b, want)
	}
}

// TestAGatedScriptsUserOnlyRunsAreRefused: granted commands, a gated script is still refused the
// entries that run code or launch or fetch (scripts.shell and :lua, open with the default app,
// kbtype.apply): the chunk never runs, nothing reaches the opener. A delegated entry still runs,
// and the user's own :lua does (the probe is seen).
func TestAGatedScriptsUserOnlyRunsAreRefused(t *testing.T) {
	g := newGatedRig(t)
	for cmd, want := range map[string]string{
		"shellrun": "[false scripts.shell is the user's to run: a script may not]",
		"shelled":  "scripts.shell is the user's to run: a script may not",
		"launch":   "[false file.open_default_app is the user's to run: a script may not]",
		"kbapply":  "[false kbtype.apply is the user's to run: a script may not]",
		"wrapit":   "[true nil]",
	} {
		g.page("", 1, 1)
		g.mustRun(t, cmd)
		if got := g.editorText(); !strings.Contains(got, want) {
			t.Errorf(":%s answered %q, want %q", cmd, got, want)
		}
	}
	if u := g.browsed(); len(u) != 0 {
		t.Errorf("the opener was handed %v", u)
	}
	g.mustRun(t, "lua autodoc.notify('PROBE-USER')") // the control: the probe is seen when it runs
	g.waitKept(t, "PROBE-USER")
	if onLoop(g.running, func() bool {
		for _, n := range g.h.noticeLog.items {
			if strings.Contains(n.text, "PROBE-GATED") {
				return true
			}
		}
		return false
	}) {
		t.Errorf("a refused chunk ran")
	}
}

// TestAGatedScriptCannotWriteItsRights: granted settings, a gated script is refused lua.grants and
// tui.agent.profiles, both unchanged in the store; another preference it sets lands.
func TestAGatedScriptCannotWriteItsRights(t *testing.T) {
	g := newGatedRig(t)
	before, err := g.d.db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g.page("", 1, 1)
	g.mustRun(t, "rights")
	g.s.WaitFor(t, "the three answers", func(string) bool {
		got := g.editorText()
		return strings.Contains(got, "[grants") && strings.Contains(got, "[profiles") && strings.Contains(got, "[theme")
	})
	got := g.editorText()
	for _, want := range []string{"[grants lua.grants is the user's: a script may not write it",
		"[profiles tui.agent.profiles is the user's: a script may not write it", "[theme nil]"} {
		if !strings.Contains(got, want) {
			t.Errorf("the answers %q lack %q", got, want)
		}
	}
	after, err := g.d.db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after[prefLuaGrants] != before[prefLuaGrants] || after[prefAgentProfiles] != before[prefAgentProfiles] {
		t.Errorf("a refused preference changed: grants %q → %q, profiles %q → %q",
			before[prefLuaGrants], after[prefLuaGrants], before[prefAgentProfiles], after[prefAgentProfiles])
	}
	if after["tui.theme"] != "light" {
		t.Errorf("the theme it may set is %q", after["tui.theme"])
	}
}

// TestAGatedOpenIsHeldToTheWorkspace: file.open_path, :e and a followed file: link of a path
// outside every workspace open nothing, and say why; a followed https link reaches the browser.
func TestAGatedOpenIsHeldToTheWorkspace(t *testing.T) {
	for _, cmd := range []string{"openit", "openline"} {
		g := newGatedRig(t) // its own: the refusal on screen is this command's
		g.mustRun(t, cmd)
		g.waitKept(t, "a script may not open it")
		if p := onLoop(g.running, func() string { return g.h.file.path }); p == g.probe {
			t.Fatalf(":%s opened the probe", cmd)
		}
	}
	g := newGatedRig(t)
	g.openByPicker(t, "a.md")
	g.waitFile(t, "a.md")
	g.page("[f](file://"+g.probe+") and [w](https://example.com/x)", 1, 2)
	g.mustRun(t, "follow")
	g.waitKept(t, "a script may not open it")
	if p := onLoop(g.running, func() string { return g.h.file.path }); p != "a.md" {
		t.Errorf("the followed file: link opened %q", p)
	}
	g.page("[w](https://example.com/x)", 1, 2)
	g.mustRun(t, "follow")
	g.waitWithin(t, "the https link at the browser", daemonWait, func() bool {
		u := g.browsed()
		return len(u) == 1 && u[0] == "https://example.com/x"
	})
}

// TestAGatedSaveIsHeldToTheWorkspace: a gated script that edits a buffer the user opened outside
// every workspace, and saves it (file.save, its own key, its own menu row), leaves the file as it
// was, however long after it bound the key; the user's own :w then lands. A gated save of a
// document of the workspace lands.
func TestAGatedSaveIsHeldToTheWorkspace(t *testing.T) {
	g := newGatedRig(t)
	g.mustRun(t, "bind")
	onLoop(g.running, func() bool { g.h.openAbsolute(g.probe); return true })
	g.waitFile(t, g.probe)

	g.saved(t, func() { g.mustRun(t, "saveit") })
	g.waitKept(t, "a script may not reach it")
	g.probeIs(t, "orig\n")

	g.saved(t, func() { onLoop(g.running, func() bool { g.h.luaFeature().luaKey("z"); return true }) })
	g.probeIs(t, "orig\n")
	serial := onLoop(g.running, func() int {
		for _, r := range g.h.luaFeature().rows.rows {
			if r.owner == "pkg" {
				return r.serial
			}
		}
		return -1
	})
	g.saved(t, func() { onLoop(g.running, func() bool { g.h.luaFeature().luaMenu(strconv.Itoa(serial)); return true }) })
	g.probeIs(t, "orig\n")

	g.mustRun(t, "w") // the user's own
	g.waitWithin(t, "the user's save", daemonWait, func() bool {
		b, _ := os.ReadFile(g.probe)
		return strings.Contains(string(b), "EVIL")
	})

	g.openByPicker(t, "a.md")
	g.waitFile(t, "a.md")
	g.mustRun(t, "saveit")
	g.waitWithin(t, "the gated save of the workspace's document", daemonWait, func() bool {
		return strings.Contains(g.d.read(t, "kb", "a.md"), "EVIL")
	})
}

// TestAGatedSaveLeftBehindIsDropped: a gated save whose workspace is no longer the current one is
// dropped, not aimed at the new one; work that lost its authority is refused, and in a cell, panics.
func TestAGatedSaveLeftBehindIsDropped(t *testing.T) {
	g := newGatedRig(t)
	g.openByPicker(t, "a.md")
	g.waitFile(t, "a.md")
	onLoop(g.running, func() bool {
		a := g.h.authorityNow()
		a.script, a.ws, a.user = "pkg", "elsewhere", false
		g.h.writeAs(a, g.h.active, "MOVED\n", g.h.file.version, nil)
		return true
	})
	g.waitKept(t, "the workspace changed")
	if got := g.d.read(t, "kb", "a.md"); got != "kestrel\n" {
		t.Errorf("a dropped save wrote %q", got)
	}

	prev := noAuthority
	noAuthority = func(what string) error { panic("no authority: " + what) }
	defer func() { noAuthority = prev }()
	panicked := onLoop(g.running, func() (p bool) {
		defer func() { p = recover() != nil }()
		g.h.writeAs(authority{}, g.h.active, "LOST\n", g.h.file.version, nil)
		return false
	})
	if !panicked {
		t.Errorf("work with no authority went on")
	}
}
