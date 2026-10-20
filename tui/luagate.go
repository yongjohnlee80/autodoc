package tui

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/script"
)

// GATED LUA SCRIPTS' GRANTS (ADR 1792004705 §4.3): a gated script (a package, M8) declares what it
// needs as it loads, autodoc.requires{…}; the program asks once, and remembers a yes in the
// lua.grants preference. Only a yes is kept: Confirm.qml's every other way out (No, Esc, closing,
// another question, quitting) runs OnCancel alike, so a refusal asks again at the next start rather
// than ever being a dismissal remembered. A call without its grant answers nil and why
// (script.Grants, script/gate.go); a grant given later works at once.

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
	if h.asker.pending {
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
		h.luaAsking = true
		next := func() { h.luaAsking = false; h.p.Post(h.askLuaGrants) }
		who := h.trf("autodoc.question.luagate.the_lua_script", a.script)
		if name, ok := strings.CutPrefix(a.script, "plugin:"); ok {
			who = h.trf("autodoc.question.luagate.the_plugin_s_lua_package", name)
		}
		h.ask(Question{
			Title: h.trf("autodoc.question.luagate.let_use", who, strings.Join(open, ", ")),
			Text:  luaGrantTextIn(h.trf, open),
			Yes:   h.tr("autodoc.question.luagate.allow"), No: h.tr("autodoc.question.luagate.not_now"),
			OnYes:    func() { h.grantLua(a.script, open); next() },
			OnCancel: next,
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

// luaGrantsOf reads the preference's JSON; anything else is no grants.
func luaGrantsOf(s string) map[string]map[string]bool {
	var g map[string]map[string]bool
	if json.Unmarshal([]byte(s), &g) != nil {
		return nil
	}
	return g
}

// luaGrantText says what each capability lets a script do, in English.
func luaGrantText(caps []string) string { return luaGrantTextIn(inEnglish{}.Trf, caps) }

// luaGrantTextIn is luaGrantText in the language trf (a Host's trf, or inEnglish{}.Trf) says it in: a
// line each, the capability's name and what it lets a script do.
func luaGrantTextIn(trf func(string, ...any) string, caps []string) string {
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
	"workspace.read":  "autodoc.question.luagate.read_the_workspace_s_documents_search_them_and_f",
	"workspace.write": "autodoc.question.luagate.write_rename_and_remove_documents_and_tasks",
	"settings":        "autodoc.question.luagate.change_autodoc_s_settings_workspaces_models_pref",
	"clipboard":       "autodoc.question.luagate.read_and_write_the_clipboard",
	"agents":          "autodoc.question.luagate.list_the_agents_and_type_into_them",
	"commands":        "autodoc.question.luagate.run_any_of_autodoc_s_commands",
}

var _ script.Grants = luaGrants{}
