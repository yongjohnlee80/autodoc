package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
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
	r.waitWithin(t, "lua.grants in the store", daemonWait, func() bool {
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

// TestAnOldDaemonsVerbsFailClosed: contracts without an effect (a daemon older than effects) give a
// gated script nothing, whatever it was granted: doc.remove is not settings' to cover.
func TestAnOldDaemonsVerbsFailClosed(t *testing.T) {
	old := decodeVerbs(map[string]any{"verbs": []any{
		map[string]any{"verb": "doc.remove", "audience": "user", "params": []any{}},
		map[string]any{"verb": "doc.read", "audience": "agent", "params": []any{}, "effect": "read"},
	}})
	if old["doc.remove"].effect != "" || old["doc.read"].effect != "read" {
		t.Fatalf("decoded %+v", old)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	onLoop(r, func() bool {
		// as an old daemon answers sys.contracts: no effects
		for k, v := range r.h.scripting.verbs {
			v.effect = ""
			r.h.scripting.verbs[k] = v
		}
		r.h.prefs.luaGrants = map[string]map[string]bool{"pkg": {"settings": true, "workspace.read": true, "workspace.write": true}}
		return true
	})
	if err := onLoop(r, func() error {
		return r.h.loadGated("pkg", "pkg.lua", []byte(`autodoc.command{ name = "rm", run = function(ed)
  local ok, err = autodoc.call("doc.remove", {autodoc.current_workspace(), "a.md", ""})
  local rok, rerr = autodoc.doc.read(autodoc.current_workspace(), "a.md")
  ed:insert(tostring(ok) .. "|" .. tostring(err) .. "|" .. tostring(rok) .. "|" .. tostring(rerr))
end }`))
	}); err != nil {
		t.Fatal(err)
	}
	r.page("", 1, 1)
	r.mustRun(t, "rm")
	r.wantText(t, ":rm on an old daemon", "nil|refused: call's effect is not known, so no grant covers it|nil|refused: read's effect is not known, so no grant covers it")
	if got := d.read(t, "kb", "a.md"); got != "kestrel\n" {
		t.Fatalf("a.md is %q: the unclassified remove ran", got)
	}
}

// A script's grant asks in a sentence per capability, one AutoDoc does not know said as such, and
// how Not now answers.
func TestALuaGrantNamesEachCapability(t *testing.T) {
	got := luaGrantText(inEnglish{}.Trf, []string{"workspace.read", "workspace.write", "settings", "clipboard", "agents", "commands", "rockets"})
	for _, want := range []string{
		"workspace.read: read the files in your workspaces, except hidden ones, search them and follow their links\n",
		"workspace.write: create, edit, rename and remove the files of this workspace, except hidden ones, and its tasks\n",
		"settings: change AutoDoc's settings: workspaces, models, preferences\n",
		"clipboard: read and write the clipboard\n",
		"agents: list the agents and send them text: an agent can run commands on your computer\n",
		"commands: run any of AutoDoc's commands\n",
		"rockets: a capability AutoDoc does not know\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the grant lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "Not now refuses until the next start.") {
		t.Errorf("the grant does not end with how Not now answers:\n%s", got)
	}
}

// grantHeard is a script that hears the grant event, as who, and retries the read it was refused:
// each answer it hears is a line to autodoc.heard (the test's own function), "<who> <allowed>
// <capabilities> <the retried read's refusal, nil once granted>".
func grantHeard(who string) string {
	return `autodoc.on("grant", function(ed, info)
  local _, err = autodoc.doc.read(autodoc.current_workspace(), "a.md", function() end)
  autodoc.heard("` + who + ` " .. tostring(info.allowed) .. " " .. table.concat(info.capabilities, ",") .. " " .. tostring(err))
end)`
}

// TestAGatedScriptHearsItsGrantAnswered: a package that asked for capabilities hears the user's
// answer as the grant event, so it can resume rather than poll: Not now and a card closed
// unanswered as allowed false, its retried read still refused; Allow as true, the grant already in
// force for the read its handler retries. One event per card; neither another gated script nor an
// ungated one hears it.
func TestAGatedScriptHearsItsGrantAnswered(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "x\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	var heard []string
	onLoop(r, func() bool {
		r.h.scripts.SetFunction("heard", func(L *lua.LState) int { heard = append(heard, L.CheckString(1)); return 0 })
		return true
	})
	if err := onLoop(r, func() error {
		if err := r.h.loadGated("other", "other.lua", []byte(grantHeard("other"))); err != nil {
			return err
		}
		if err := r.h.scripts.Load("mine.lua", []byte(grantHeard("mine"))); err != nil {
			return err
		}
		return r.h.loadGated("pkg", "pkg.lua", []byte(`autodoc.requires{"workspace.read", "clipboard"}
`+grantHeard("pkg")))
	}); err != nil {
		t.Fatal(err)
	}
	got := func() []string { return onLoop(r, func() []string { return slices.Clone(heard) }) }
	for i, end := range []struct {
		name   string
		answer func()
		want   string
	}{
		{"closed", func() { r.h.asker.closed() }, "pkg false workspace.read,clipboard needs workspace.read: not granted"},
		{"not now", func() { r.h.asker.rejected(); r.h.asker.closed() }, "pkg false workspace.read,clipboard needs workspace.read: not granted"},
		{"allow", func() { r.h.asker.accepted(); r.h.asker.closed() }, "pkg true workspace.read,clipboard nil"},
	} {
		if i > 0 { // Not now is not remembered: the package's requires asks again, as at its next start
			onLoop(r, func() bool { luaGrants{r.h}.Request("pkg", []string{"workspace.read", "clipboard"}); return true })
		}
		r.s.WaitForText(t, "let the Lua script pkg use workspace.read, clipboard?")
		onLoop(r, func() bool { end.answer(); return true })
		r.s.WaitFor(t, end.name, func(string) bool { return len(got()) > i })
		if h := got(); len(h) != i+1 || h[i] != end.want {
			t.Fatalf("%s: heard %q, want one more, %q", end.name, h, end.want)
		}
	}
	r.s.WaitFor(t, "the grant kept", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.luaGrants["pkg"]["clipboard"] })
	})
	if h := got(); len(h) != 3 {
		t.Fatalf("heard %q after three cards: another script heard the package's answer, or one card answered twice", h)
	}
}

// TestAGrantHandlerThatAsksAgainGetsItsOwnCard: a grant handler that asks for another capability
// (requires inside the event, while its card is still the one shown) gets a card of its own once
// this one closes, and hears that card's answer too: the re-entry neither replaces the open card
// nor is lost.
func TestAGrantHandlerThatAsksAgainGetsItsOwnCard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "x\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	var heard []string
	onLoop(r, func() bool {
		r.h.scripts.SetFunction("heard", func(L *lua.LState) int { heard = append(heard, L.CheckString(1)); return 0 })
		return true
	})
	if err := onLoop(r, func() error {
		return r.h.loadGated("pkg", "pkg.lua", []byte(`autodoc.requires{"workspace.read"}
autodoc.on("grant", function(ed, info)
  autodoc.heard(tostring(info.allowed) .. " " .. table.concat(info.capabilities, ","))
  if info.capabilities[1] == "workspace.read" then autodoc.requires{"clipboard"} end
end)`))
	}); err != nil {
		t.Fatal(err)
	}
	got := func() []string { return onLoop(r, func() []string { return slices.Clone(heard) }) }
	r.s.WaitForText(t, "let the Lua script pkg use workspace.read?")
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.asker.closed(); return true })
	r.s.WaitForText(t, "let the Lua script pkg use clipboard?")
	if h := got(); len(h) != 1 || h[0] != "true workspace.read" {
		t.Fatalf("after the first card: heard %q, want its one answer", h)
	}
	onLoop(r, func() bool { r.h.asker.rejected(); r.h.asker.closed(); return true })
	r.s.WaitFor(t, "the second card's answer", func(string) bool { return len(got()) > 1 })
	if h := got(); len(h) != 2 || h[1] != "false clipboard" {
		t.Fatalf("after the second card: heard %q, want its answer, false clipboard", h)
	}
	if !onLoop(r, func() bool { return r.h.prefs.luaGrants["pkg"]["workspace.read"] && !r.h.prefs.luaGrants["pkg"]["clipboard"] }) {
		t.Fatalf("grants %v, want workspace.read only", onLoop(r, func() map[string]bool { return r.h.prefs.luaGrants["pkg"] }))
	}
}
