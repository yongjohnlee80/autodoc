package tui

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/script"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// GATED LUA SCRIPTS' GRANTS (docs/decisions/contract.md#grants-are-remembered-as-a-yes): a gated
// script (a package, M8) declares what it needs as it loads, autodoc.requires{…}; the program asks
// once, and remembers a yes in the lua.grants preference. Only a yes is kept: Confirm.qml's every
// other way out (No, Esc, closing, another question, quitting) runs OnCancel alike, so a refusal
// asks again at the next start rather than ever being a dismissal remembered. A call without its
// grant answers nil and why (script.Grants, script/gate.go); a grant given later works at once.

// luaAsk is one script's requires{…}, waiting to be asked.
type luaAsk struct {
	script string
	caps   []string
}

// luaGrants is script.Grants over the preference.
type luaGrants struct{ h *Host }

func (g luaGrants) Granted(script, capability string) bool {
	return g.h.prefs.luaGrants[script][capability]
}

// Request queues the question, asked once the file has loaded and the answers given are known.
func (g luaGrants) Request(script string, caps []string) {
	if a := g.h.luaShown; a != nil && a.script == script && slices.Equal(a.caps, caps) {
		return // its card is open: the one answer it gets answers this retry too
	}
	for _, a := range g.h.luaAsks {
		if a.script == script && slices.Equal(a.caps, caps) {
			return // asked already, not answered yet: an agent retrying asks once
		}
	}
	g.h.luaAsks = append(g.h.luaAsks, luaAsk{script, caps})
	g.h.p.Post(g.h.askLuaGrants)
}

// loadGated loads one gated script under the user's grants.
func (h *Host) loadGated(script, name string, src []byte) error {
	return h.scripts.LoadGated(script, name, src, luaGrants{h})
}

// askLuaGrants asks the first waiting script about the capabilities it has no grant for, one card
// at a time: its answer asks the next.
func (h *Host) askLuaGrants() {
	if !h.luaPrefsRead || h.luaAsking {
		return
	}
	if h.asker.Pending {
		return // another card is open: a grant waits its turn rather than replace it (onIdle resumes)
	}
	for len(h.luaAsks) > 0 {
		a := h.luaAsks[0]
		h.luaAsks = h.luaAsks[1:]
		var open []string
		for _, c := range a.caps {
			if !h.prefs.luaGrants[a.script][c] {
				open = append(open, c)
			}
		}
		if len(open) == 0 {
			continue
		}
		h.luaAsking, h.luaShown = true, &a
		next := func() { h.luaAsking, h.luaShown = false, nil; h.p.Post(h.askLuaGrants) }
		who := h.trf("autodoc.question.luagate.the_lua_script", a.script)
		if name, ok := strings.CutPrefix(a.script, "plugin:"); ok {
			who = h.trf("autodoc.question.luagate.the_plugin_s_lua_package", name)
		} else if name, ok := strings.CutPrefix(a.script, "agent:"); ok { // the editor socket's (editorsocket.go ungranted)
			who = h.trf("autodoc.question.luagate.the_agent", name)
		}
		h.ask(feature.Question{
			Title: h.trf("autodoc.question.luagate.let_use", who, strings.Join(open, ", ")),
			Text:  luaGrantText(h.trf, open),
			Yes:   h.tr("autodoc.question.luagate.allow"), No: h.tr("autodoc.question.luagate.not_now"),
			OnYes:    func() { h.grantLua(a.script, open); h.grantAnswered(a.script, open, true); next() },
			OnCancel: func() { h.grantAnswered(a.script, open, false); next() },
		})
		return
	}
}

// grantLua remembers that script may use caps.
func (h *Host) grantLua(script string, caps []string) {
	all := map[string]map[string]bool{}
	for s, g := range h.prefs.luaGrants {
		all[s] = maps.Clone(g)
	}
	if all[script] == nil {
		all[script] = map[string]bool{}
	}
	for _, c := range caps {
		all[script][c] = true
	}
	b, _ := json.Marshal(all)
	h.setPref(prefLuaGrants, string(b), func(p *prefs) { p.luaGrants = all })
}

// revokeLua takes caps back from script (docs/decisions/contract.md#a-grant-can-be-taken-back): its
// next call needing one is refused and asks again, as a first one does. A holder left with none is
// dropped from the preference.
func (h *Host) revokeLua(script string, caps []string) {
	all := map[string]map[string]bool{}
	for s, g := range h.prefs.luaGrants {
		all[s] = maps.Clone(g)
	}
	for _, c := range caps {
		delete(all[script], c)
	}
	if len(all[script]) == 0 {
		delete(all, script)
	}
	b, _ := json.Marshal(all)
	h.setPref(prefLuaGrants, string(b), func(p *prefs) { p.luaGrants = all })
}

// RevokeGrants is revokeLua, for Agent permissions (grants.go).
func (c core) RevokeGrants(holder string, caps []string) { c.h.revokeLua(holder, caps) }

// luaGrantsOf reads the preference's JSON; anything else is no grants.
func luaGrantsOf(s string) map[string]map[string]bool {
	var g map[string]map[string]bool
	if json.Unmarshal([]byte(s), &g) != nil {
		return nil
	}
	return g
}

// luaGrantText says what each capability lets a script do, a line each, in the language trf (a
// Host's trf, or inEnglish{}.Trf) says it in.
func luaGrantText(trf func(string, ...any) string, caps []string) string {
	var b strings.Builder
	for _, c := range caps {
		id, ok := luaCapabilityText[c]
		if !ok {
			id = "autodoc.question.luagate.a_capability_autodoc_does_not_know"
		}
		fmt.Fprintf(&b, "%s: %s\n", c, trf(id))
	}
	b.WriteString(trf("autodoc.question.luagate.not_now_refuses_until_the_next_start"))
	return b.String()
}

// luaCapabilityText is what each capability lets a script do, by its catalog id.
var luaCapabilityText = map[string]string{
	"workspace.read":  "autodoc.question.luagate.read_the_files_in_your_workspaces_except_hidden",
	"workspace.write": "autodoc.question.luagate.create_edit_rename_and_remove_the_files_of_this",
	"settings":        "autodoc.question.luagate.change_autodoc_s_settings_workspaces_models_pref",
	"clipboard":       "autodoc.question.luagate.read_and_write_the_clipboard",
	"agents":          "autodoc.question.luagate.list_the_agents_and_send_them_text_an_agent_can",
	"commands":        "autodoc.question.luagate.run_any_of_autodoc_s_commands",
	"terminal":        "autodoc.question.luagate.run_command_lines_in_autodoc_s_terminal",
	// a plugin's start with AutoDoc, its manifest's start = "launch" (pluginlaunch.go)
	launchCapability: "autodoc.question.pluginlaunch.start_with_autodoc",
	// the editor socket's (editorsocket.go editorVerbs): an agent's reach into the page
	"editor.read":  "autodoc.question.luagate.read_your_open_documents_and_find_in_them",
	"editor.write": "autodoc.question.luagate.insert_replace_and_delete_text_in_your_open_documents",
}

var _ script.Grants = luaGrants{}

// grantAnswered tells the requester the user's answer: an agent (agent:<codename>), by the crew,
// as the user's own mail, so it retries the refused call, or stops, instead of retrying blind
// (crew.grantAnswered); a window with no crew scope says nothing, and the refusal it gave says so
// too (editorVerb). A gated Lua script or package (plugin:<name> for a plugin's) hears the grant
// event, its own handlers only (luaevents.go), so it can resume what the refusal stopped.
func (h *Host) grantAnswered(requester string, caps []string, allowed bool) {
	if h.onGrantAnswered != nil {
		h.onGrantAnswered(requester, caps, allowed)
	}
	if codename, ok := strings.CutPrefix(requester, "agent:"); ok {
		if h.crew != nil {
			h.crew.grantAnswered(codename, caps, allowed)
		}
		return
	}
	h.emitLuaFor(requester, "grant", map[string]any{"capabilities": caps, "allowed": allowed})
}

// crewHeld is the window's crew scope, when it holds one.
func (h *Host) crewHeld() (scope string, epoch int64, ok bool) {
	if h.crew == nil {
		return "", 0, false
	}
	return h.crew.held()
}
