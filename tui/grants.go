package tui

import (
	"cmp"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// AGENT PERMISSIONS — Go › Agent permissions…, :permissions, a feature of its own
// (docs/decisions/contract.md#a-grant-can-be-taken-back): every capability the user granted, one row
// each, as the lua.grants preference keeps them, whoever holds it: an agent (agent:<codename>, the
// editor socket's), a plugin's Lua package (plugin:<name>) or a gated script. Revoke takes the row's
// capability back, Revoke all everything its holder holds, each asked first. A capability taken
// back is refused at its holder's next use and asked for again by its card, as a first use is: the
// dialog never grants, so a grant is still given only where the user sees what asks for it. The list
// follows the preference while the dialog is open: a card answered meanwhile shows at once.

// grantsCore is what Agent permissions needs of the program.
type grantsCore interface {
	Prefs() PrefsView
	Set(name string, v any)
	Open(id string)
	Focus(id string)
	Ask(q Question) Asked
	Notify(msg string)
	Tr(id string) string
	Trf(id string, args ...any) string
	// RevokeGrants takes caps back from holder, in the lua.grants preference.
	RevokeGrants(holder string, caps []string)
}

// grantsListID is the dialog's list, as an id inside a dialog is named: its own id @ the dialog's.
const grantsListID = "grantTable@grants"

// grantRow is one capability one holder holds.
type grantRow struct{ holder, capability string }

func (r grantRow) key() string { return r.holder + "\x00" + r.capability }

// grantsView is the dialog. Its fields are the loop's.
type grantsView struct {
	c      grantsCore
	rows   *rows[grantRow]
	shown  bool // the dialog is open
	cursor int
	key    string // the row under the cursor, kept when the list is read again
}

func newGrantsView(c grantsCore) *grantsView {
	g := &grantsView{c: c}
	g.rows = newRows(tuidecl.NewListModel("key", "who", "kind", "capability", "what"), g.rowOf)
	return g
}

func (*grantsView) Name() string { return "grants" }

func (g *grantsView) State() map[string]any {
	return map[string]any{"App.grants": g.rows.Model(), "App.grantsIndex": -1, "App.grantsDetail": "", "App.grantsStatus": ""}
}

func (g *grantsView) Commands() map[string]Command {
	return map[string]Command{
		"App.openGrants":     none(g.open),
		"App.grantsClosed":   none(g.closed),
		"App.grantShown":     oneNumber("a row", g.showDetail),
		"App.grantRevoke":    oneNumber("a row", g.revoke),
		"App.grantRevokeAll": oneNumber("a row", g.revokeAll),
	}
}

// Catalog is the dialog's user command, :permissions: it runs what App.openGrants runs.
func (g *grantsView) Catalog() []Entry {
	e := builtin("agents.permissions", "agent permissions: what each agent and package was granted", "App.openGrants()", g.open)
	e.Verb = "permissions"
	return []Entry{e}
}

// PreferencesChanged: a grant given or taken back while the dialog is open shows at once.
func (g *grantsView) PreferencesChanged() {
	if g.shown {
		g.list()
	}
}

// open lists the grants and opens the dialog, the keyboard on the list, its cursor on the first.
func (g *grantsView) open() {
	g.shown, g.cursor, g.key = true, 0, ""
	g.list()
	g.c.Open("grants")
	g.c.Focus(grantsListID) // the list holds the keys: arrows move it, the buttons answer their letters
}

// closed is the dialog dismissed: the preference's changes stop listing it again.
func (g *grantsView) closed() { g.shown = false }

// grantRows are every capability granted, by holder (agents first, then packages, then scripts),
// then by capability.
func grantRows(all map[string]map[string]bool) []grantRow {
	var out []grantRow
	for holder, caps := range all {
		for c, yes := range caps {
			if yes {
				out = append(out, grantRow{holder, c})
			}
		}
	}
	slices.SortFunc(out, func(a, b grantRow) int {
		return cmp.Or(cmp.Compare(holderRank(a.holder), holderRank(b.holder)), cmp.Compare(a.holder, b.holder),
			cmp.Compare(a.capability, b.capability))
	})
	return out
}

// holderRank orders the holders: agents, then plugins' packages, then gated scripts.
func holderRank(holder string) int {
	switch {
	case strings.HasPrefix(holder, "agent:"):
		return 0
	case strings.HasPrefix(holder, "plugin:"):
		return 1
	}
	return 2
}

// list reads the grants again, the cursor kept on its row (else at the place it was), and says how
// many there are.
func (g *grantsView) list() {
	items := grantRows(asPrefs(g.c.Prefs()).luaGrants)
	g.rows.Reset(items)
	if i := slices.IndexFunc(items, func(r grantRow) bool { return r.key() == g.key }); g.key != "" && i >= 0 {
		g.cursor = i
	}
	g.cursor = min(max(g.cursor, 0), max(len(items)-1, 0))
	g.c.Set("App.grantsIndex", -1) // moved away first, so the same row twice still moves the table's cursor
	g.c.Set("App.grantsIndex", g.cursor)
	holders := map[string]bool{}
	for _, r := range items {
		holders[r.holder] = true
	}
	if len(items) == 0 {
		g.c.Set("App.grantsStatus", g.c.Tr("autodoc.text.grants.none"))
	} else {
		g.c.Set("App.grantsStatus", g.c.Trf("autodoc.text.grants.count", len(items), len(holders)))
	}
	g.showDetail(g.cursor)
}

// who is a holder as the list names it, and what kind of holder it is.
func (g *grantsView) who(holder string) (name, kind string) {
	if n, ok := strings.CutPrefix(holder, "agent:"); ok {
		return n, g.c.Tr("autodoc.text.grants.kind_agent")
	}
	if n, ok := strings.CutPrefix(holder, "plugin:"); ok {
		return n, g.c.Tr("autodoc.text.grants.kind_package")
	}
	return holder, g.c.Tr("autodoc.text.grants.kind_script")
}

// what is what a capability lets its holder do, as its card says it.
func (g *grantsView) what(capability string) string {
	id, ok := luaCapabilityText[capability]
	if !ok {
		id = "autodoc.question.luagate.a_capability_autodoc_does_not_know"
	}
	return g.c.Tr(id)
}

func (g *grantsView) rowOf(_ int, r grantRow) rowOf {
	name, kind := g.who(r.holder)
	return rowOf{"key": r.key(), "who": name, "kind": kind, "capability": r.capability, "what": g.what(r.capability)}
}

// showDetail shows row i under the list: who holds what, and what it lets them do.
func (g *grantsView) showDetail(i int) {
	r, ok := g.rows.At(i)
	if !ok {
		g.c.Set("App.grantsDetail", "")
		return
	}
	g.cursor, g.key = i, r.key()
	name, kind := g.who(r.holder)
	g.c.Set("App.grantsDetail", g.c.Trf("autodoc.text.grants.detail", name, kind, r.capability, g.what(r.capability)))
}

// revoke takes row i's capability back from its holder, once the user agrees.
func (g *grantsView) revoke(i int) {
	r, ok := g.rows.At(i)
	if !ok {
		return
	}
	name, _ := g.who(r.holder)
	g.c.Ask(Question{
		Title: g.c.Trf("autodoc.question.grants.revoke_title", r.capability, name),
		Text:  g.c.Trf("autodoc.question.grants.revoke_text", name, r.capability, g.what(r.capability)),
		Yes:   g.c.Tr("autodoc.question.grants.revoke"), No: g.c.Tr("autodoc.question.grants.keep"),
		OnYes: func() {
			g.c.RevokeGrants(r.holder, []string{r.capability})
			g.c.Notify(g.c.Trf("autodoc.notice.grants.revoked", name, r.capability))
		},
	})
}

// revokeAll takes back everything row i's holder holds, once the user agrees.
func (g *grantsView) revokeAll(i int) {
	r, ok := g.rows.At(i)
	if !ok {
		return
	}
	var caps []string
	for _, x := range g.rows.All() {
		if x.holder == r.holder {
			caps = append(caps, x.capability)
		}
	}
	name, _ := g.who(r.holder)
	all := strings.Join(caps, ", ")
	g.c.Ask(Question{
		Title: g.c.Trf("autodoc.question.grants.revoke_all_title", name),
		Text:  g.c.Trf("autodoc.question.grants.revoke_all_text", name, all),
		Yes:   g.c.Tr("autodoc.question.grants.revoke"), No: g.c.Tr("autodoc.question.grants.keep"),
		OnYes: func() {
			g.c.RevokeGrants(r.holder, caps)
			g.c.Notify(g.c.Trf("autodoc.notice.grants.revoked", name, all))
		},
	})
}
