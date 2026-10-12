package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// ENDPOINTS — a list of model services the daemon stores, as one of AI models' tabs shows it: the
// embedding providers (providers.go) and the rankers (rankers.go). Each is listed with the one in
// use and why a choice was refused, each entry's usage and recent calls, Use, Remove after asking,
// and one form that adds or edits an entry: a kind chooser, a key that goes in and is never shown
// again, and a Check that asks the endpoint what it serves. What a list does differently is its
// spec's hooks; the rest is here, once.

// endpointKind is one kind a form offers: the store's name, its label, where it usually is, the
// key field's label ("" for a kind that takes no key), whether it is sent a context window, and
// whether its model is typed.
type endpointKind struct {
	kind, label, base, keyLabel string
	window, model               bool
}

// endpointRow is an entry as the list and the form know it; context, the window in tokens, is a
// provider's alone.
type endpointRow struct {
	name, kind, base, model string
	context                 int64
	hasKey                  bool
}

// endpointSpec is one list: its verbs and sources, its kinds, and what it does differently.
type endpointSpec struct {
	// noun names its sources (App.<noun>s, App.<noun>Detail, …), its form (<noun>Edit) and its
	// messages; verb prefixes its daemon verbs (<verb>.use, .usage, .log, .add, .update, .remove,
	// .models); list is the verb that lists them, answering them under <noun>s
	noun, verb, list string
	// ranker: a change of the list is a change of search's second stage (Core.SearchModelsChanged)
	ranker              bool
	kinds               []endpointKind
	blank               endpointRow // what Add… starts from
	addTitle, checkHint string      // the form's title on Add…, and what Check does, before it is asked
	columns             []string    // the list's columns after its key

	// listed is the status line under the list, from the list's answer, the one in use and why the
	// one chosen was refused
	listed func(m map[string]any, active, why string) string
	// removal asks before removing r; remove removes it
	removal func(r endpointRow, remove func())
	// the optional hooks: the form's own fields as it opens on r; the fields a kind shows or hides;
	// what the form says of Check's answer; and the form's own fields put into spec before a save,
	// answering why it is not saved ("" when it is)
	formOpened func(r endpointRow)
	kindShown  func(k endpointKind)
	checked    func(models []string, err error)
	saveSpec   func(spec map[string]any, k endpointKind, extra string) string
}

// endpoints is one list and its form.
type endpoints struct {
	c      Core
	spec   endpointSpec
	rows   *rows[endpointRow]
	active string // the one in use
	cursor int    // the row under the list's cursor: the one the dialog's shared buttons act on
	seq    uint64 // numbers the detail's reads
	// the form: the entry it edits ("" a new one), the kind chosen, and whether the entry it edits
	// has a key sealed
	editing string
	kind    int
	hasKey  bool
}

func newEndpoints(c Core, spec endpointSpec) *endpoints {
	e := &endpoints{c: c, spec: spec}
	e.rows = newRows(tuidecl.NewListModel(append([]string{"key"}, spec.columns...)...), e.rowOf)
	return e
}

// src is one of the list's sources: App.provider + Detail.
func (e *endpoints) src(name string) string { return "App." + e.spec.noun + name }

// State is the list's sources and the form's, as both kinds spell them.
func (e *endpoints) State() map[string]any {
	labels := make([]string, len(e.spec.kinds))
	for i, k := range e.spec.kinds {
		labels[i] = k.label
	}
	state := map[string]any{e.src("s"): e.rows.Model(), e.src("Kinds"): choices(labels...), e.src("KindIndex"): 0}
	for _, name := range []string{"sStatus", "Detail", "FormTitle", "FormError", "Name", "Base", "Model", "Key", "KeyLabel", "ModelsStatus"} {
		state[e.src(name)] = ""
	}
	return state
}

func (e *endpoints) kindIndex(kind string) int {
	for i, k := range e.spec.kinds {
		if k.kind == kind {
			return i
		}
	}
	return 0
}

func (e *endpoints) at(i int) (endpointRow, bool) { return e.rows.At(i) }

// load lists the entries, the one in use, and why the one chosen is not in use, if it is not.
func (e *endpoints) load() {
	tk := e.c.Ticket(nil, TicketSession)
	type answer struct {
		rows []endpointRow
		m    map[string]any
		err  error
	}
	e.c.Go(func(ctx context.Context) any {
		res, err := e.c.Call(ctx, e.spec.list)
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		var rows []endpointRow
		for _, x := range asList(m[e.spec.noun+"s"]) {
			p := asMap(x)
			has, _ := p["has_key"].(bool)
			rows = append(rows, endpointRow{name: str(p, "name"), kind: str(p, "kind"), base: str(p, "base_url"), model: str(p, "model"), context: num(p, "context"), hasKey: has})
		}
		return answer{rows: rows, m: m}
	}, func(v any) {
		a := v.(answer)
		if !tk.Current() {
			return
		}
		if a.err != nil {
			e.c.Set(e.src("sStatus"), e.spec.noun+"s: "+wireMessage(a.err))
			return
		}
		e.active = str(a.m, "active")
		e.rows.Reset(a.rows)
		e.c.Set(e.src("sStatus"), e.spec.listed(a.m, e.active, str(a.m, "error")))
		if len(a.rows) > 0 {
			e.detail(min(max(e.cursor, 0), len(a.rows)-1))
		} else {
			e.c.Set(e.src("Detail"), "")
		}
	})
}

// rowOf is an entry as the list shows it, the one in use marked.
func (e *endpoints) rowOf(_ int, r endpointRow) rowOf {
	use := ""
	if r.name == e.active {
		use = "●"
	}
	key := "—"
	if r.hasKey {
		key = "sealed"
	}
	model := r.model
	if model == "" {
		model = "—" // a TEI server's own, named by Check
	}
	k := e.spec.kinds[e.kindIndex(r.kind)]
	row := rowOf{"key": r.name, "use": use, "name": r.name, "kind": k.label, "model": model, "apiKey": key}
	if slices.Contains(e.spec.columns, "context") {
		window := "—"
		if k.window {
			window = strconv.FormatInt(r.context, 10)
		}
		row["context"] = window
	}
	return row
}

// detail shows entry i's usage over the last week and its latest calls, and keeps it as the row
// the dialog's shared buttons act on.
func (e *endpoints) detail(i int) {
	r, ok := e.at(i)
	if !ok {
		return
	}
	e.cursor = i
	tk := e.c.Ticket(&e.seq, TicketSession)
	e.c.Go(func(ctx context.Context) any {
		us, err := e.c.Call(ctx, e.spec.verb+".usage", r.name, int64(7))
		if err != nil {
			return answerOf[string]{err: err}
		}
		log, err := e.c.Call(ctx, e.spec.verb+".log", r.name, int64(30))
		if err != nil {
			return answerOf[string]{err: err}
		}
		return answerOf[string]{v: usageText(r.name, asList(us), asList(log))}
	}, func(v any) {
		a := v.(answerOf[string])
		if !tk.Current() {
			return
		}
		if a.err != nil {
			e.c.Set(e.src("Detail"), r.name+": "+wireMessage(a.err))
			return
		}
		e.c.Set(e.src("Detail"), a.v)
	})
}

// usageText is an entry's usage, a day a line, and its latest calls.
func usageText(name string, usage, log []any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — the last 7 days", name)
	if len(usage) == 0 {
		b.WriteString(": no calls yet")
	}
	for _, x := range usage {
		u := asMap(x)
		fmt.Fprintf(&b, "\n  %s  %d requests · %d texts · %d tokens", str(u, "day"), num(u, "requests"), num(u, "texts"), num(u, "tokens"))
		if f := num(u, "failures"); f > 0 {
			fmt.Fprintf(&b, " · %d failed", f)
		}
		if l := num(u, "limited"); l > 0 {
			fmt.Fprintf(&b, " (%d at the usage limit)", l)
		}
	}
	if len(log) > 0 {
		b.WriteString("\nlatest calls")
	}
	for _, x := range log {
		ev := asMap(x)
		at := time.Unix(num(ev, "at"), 0).Format("01-02 15:04:05")
		fmt.Fprintf(&b, "\n  %s  %d texts · %d tokens · %d ms · %s", at, num(ev, "texts"), num(ev, "tokens"), num(ev, "millis"), str(ev, "outcome"))
	}
	return b.String()
}

// choose makes name the one in use ("" none): refused says why it was not, and the one in use
// stays; used runs once it is, and the list and search follow.
func (e *endpoints) choose(name string, refused func(error), used func()) {
	e.c.Go(func(ctx context.Context) any {
		_, err := e.c.Call(ctx, e.spec.verb+".use", name)
		return err
	}, func(v any) {
		if err, _ := v.(error); err != nil {
			refused(err)
			return
		}
		used()
		e.load()
		e.c.SearchModelsChanged(e.spec.ranker)
	})
}

// startRemove asks before removing entry i.
func (e *endpoints) startRemove(i int) {
	r, ok := e.at(i)
	if !ok {
		return
	}
	e.spec.removal(r, func() { e.removeConfirmed(r.name) })
}

func (e *endpoints) removeConfirmed(name string) {
	e.c.Go(func(ctx context.Context) any {
		_, err := e.c.Call(ctx, e.spec.verb+".remove", name)
		return err
	}, func(v any) {
		if err, _ := v.(error); err != nil {
			e.c.Failed("remove "+name, err)
			return
		}
		e.c.Notify("removed the " + e.spec.noun + " " + name)
		e.load()
		e.c.SearchModelsChanged(e.spec.ranker)
	})
}

// The form.

// startAdd opens the form on a new entry, the spec's blank to begin with.
func (e *endpoints) startAdd() {
	e.editing = ""
	e.fill(e.spec.addTitle, e.spec.blank)
}

// startEdit opens the form on entry i; its key is kept unless one is typed.
func (e *endpoints) startEdit(i int) {
	r, ok := e.at(i)
	if !ok {
		return
	}
	e.editing = r.name
	e.fill("edit "+r.name, r)
}

func (e *endpoints) fill(title string, r endpointRow) {
	e.kind, e.hasKey = e.kindIndex(r.kind), r.hasKey
	e.c.Set(e.src("FormTitle"), title)
	e.c.Set(e.src("FormError"), "")
	e.c.Set(e.src("KindIndex"), e.kind)
	setField(e.c, e.src("Name"), r.name)
	setField(e.c, e.src("Base"), r.base)
	setField(e.c, e.src("Model"), r.model)
	setField(e.c, e.src("Key"), "")
	if e.spec.formOpened != nil {
		e.spec.formOpened(r)
	}
	e.showKind()
	e.c.Set(e.src("ModelsStatus"), e.spec.checkHint)
	e.c.Open(e.spec.noun + "Edit")
}

// showKind shows the fields of the kind chosen, and says what leaving the key empty does.
func (e *endpoints) showKind() {
	k := e.spec.kinds[e.kind]
	label := k.keyLabel
	if label != "" && e.editing != "" && e.hasKey {
		label = "API key (sealed; leave empty to keep it)"
	}
	e.c.Set(e.src("KeyLabel"), label)
	if e.spec.kindShown != nil {
		e.spec.kindShown(k)
	}
}

// kindChosen is the form's kind chooser: its base URL filled in when the one there is another
// kind's default, and the kind's fields shown.
func (e *endpoints) kindChosen(i int, base string) {
	if i < 0 || i >= len(e.spec.kinds) {
		return
	}
	e.kind = i
	for _, k := range e.spec.kinds {
		if strings.TrimSpace(base) == "" || base == k.base {
			setField(e.c, e.src("Base"), e.spec.kinds[i].base)
			break
		}
	}
	e.showKind()
}

// check asks the endpoint the form describes what it serves: the form's kind and base URL, an
// edited entry lending its stored key when none is typed.
func (e *endpoints) check(base, key string) {
	spec := map[string]any{"kind": e.spec.kinds[e.kind].kind, "base_url": strings.TrimSpace(base)}
	args := []any{spec}
	if key != "" {
		spec["key"] = key
	} else if e.editing != "" {
		args = []any{e.editing, spec} // its stored key, opened by the daemon
	}
	e.c.Set(e.src("ModelsStatus"), "asking the "+e.spec.noun+"…")
	tk := e.c.Ticket(nil, TicketSession)
	e.c.Go(func(ctx context.Context) any {
		res, err := e.c.Call(ctx, e.spec.verb+".models", args...)
		if err != nil {
			return answerOf[[]string]{err: err}
		}
		var out []string
		for _, m := range asList(res) {
			if s, ok := m.(string); ok {
				out = append(out, s)
			}
		}
		return answerOf[[]string]{v: out}
	}, func(v any) {
		a := v.(answerOf[[]string])
		if tk.Current() && e.spec.checked != nil {
			e.spec.checked(a.v, a.err)
		}
	})
}

// save adds the entry the form describes, or rewrites the one it edits; extra is the form's own
// field, as the spec's saveSpec reads it. A refusal opens the form again, saying why.
func (e *endpoints) save(name, base, model, key, extra string) {
	k := e.spec.kinds[e.kind]
	spec := map[string]any{"name": strings.TrimSpace(name), "kind": k.kind, "base_url": strings.TrimSpace(base), "model": strings.TrimSpace(model)}
	if e.spec.saveSpec != nil {
		if why := e.spec.saveSpec(spec, k, extra); why != "" {
			e.refuse(why)
			return
		}
	}
	switch {
	case k.keyLabel == "":
		spec["key"] = "" // a kind that takes none keeps none
	case key != "":
		spec["key"] = key
	}
	editing := e.editing
	e.c.Go(func(ctx context.Context) any {
		var err error
		if editing == "" {
			_, err = e.c.Call(ctx, e.spec.verb+".add", spec)
		} else {
			_, err = e.c.Call(ctx, e.spec.verb+".update", editing, spec)
		}
		return err
	}, func(v any) {
		if err, _ := v.(error); err != nil {
			e.refuse("not saved: " + wireMessage(err))
			return
		}
		e.c.Notify("saved the " + e.spec.noun + " " + spec["name"].(string))
		e.load()
		e.c.SearchModelsChanged(e.spec.ranker)
	})
}

// refuse opens the form again as it was (the Save button closed it), saying why.
func (e *endpoints) refuse(why string) {
	e.c.Set(e.src("FormError"), why)
	e.c.Open(e.spec.noun + "Edit")
}

// setField writes a form field bound to name, moving it away first so the same text written twice
// still reaches a field the user has typed in.
func setField(c Core, name, text string) {
	c.Set(name, "\x00")
	c.Set(name, text)
}
