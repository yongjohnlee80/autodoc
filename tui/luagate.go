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
		h.ask(Question{
			Title: fmt.Sprintf("let the Lua script %s use %s?", a.script, strings.Join(open, ", ")),
			Text:  luaGrantText(open),
			Yes:   "&Allow", No: "&Not now",
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

// luaGrantText says what each capability lets a script do.
func luaGrantText(caps []string) string {
	what := map[string]string{
		"workspace.read":  "read the workspace's documents, search them and follow their links",
		"workspace.write": "write, rename and remove documents and tasks",
		"settings":        "change AutoDoc's settings: workspaces, models, preferences",
		"clipboard":       "read and write the clipboard",
		"agents":          "list the agents and type into them",
		"commands":        "run any of AutoDoc's commands",
	}
	var b strings.Builder
	for _, c := range caps {
		d := what[c]
		if d == "" {
			d = "a capability AutoDoc does not know"
		}
		fmt.Fprintf(&b, "%s: %s\n", c, d)
	}
	b.WriteString("Not now refuses until the next start.")
	return b.String()
}

var _ script.Grants = luaGrants{}
