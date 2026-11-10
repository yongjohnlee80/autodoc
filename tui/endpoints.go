package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/feature"
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
	c      feature.Core
	spec   endpointSpec
	rows   *feature.Rows[endpointRow]
	active string // the one in use
	cursor int    // the row under the list's cursor: the one the dialog's shared buttons act on
	seq    uint64 // numbers the detail's reads
	// the form: the entry it edits ("" a new one), the kind chosen, and whether the entry it edits
	// has a key sealed
	editing string
	kind    int
	hasKey  bool
}

func newEndpoints(c feature.Core, spec endpointSpec) *endpoints {
	e := &endpoints{c: c, spec: spec}
	e.rows = feature.NewRows(tuidecl.NewListModel(append([]string{"key"}, spec.columns...)...), e.rowOf)
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
	state := map[string]any{e.src("s"): e.rows.Model(), e.src("Kinds"): feature.Choices(labels...), e.src("KindIndex"): 0}
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
	tk := e.c.Ticket(nil, feature.TicketSession)
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
		m := feature.AsMap(res)
		var rows []endpointRow
		for _, x := range feature.AsList(m[e.spec.noun+"s"]) {
			p := feature.AsMap(x)
			has, _ := p["has_key"].(bool)
			rows = append(rows, endpointRow{name: feature.Str(p, "name"), kind: feature.Str(p, "kind"), base: feature.Str(p, "base_url"), model: feature.Str(p, "model"), context: feature.Num(p, "context"), hasKey: has})
		}
		return answer{rows: rows, m: m}
	}, func(v any) {
		a := v.(answer)
		if !tk.Current() {
			return
		}
		if a.err != nil {
			e.c.Set(e.src("sStatus"), e.spec.noun+"s: "+feature.WireMessage(a.err))
			return
		}
		e.active = feature.Str(a.m, "active")
		e.rows.Reset(a.rows)
		e.c.Set(e.src("sStatus"), e.spec.listed(a.m, e.active, feature.Str(a.m, "error")))
		if len(a.rows) > 0 {
			e.detail(min(max(e.cursor, 0), len(a.rows)-1))
		} else {
			e.c.Set(e.src("Detail"), "")
		}
	})
}

// rowOf is an entry as the list shows it, the one in use marked.
func (e *endpoints) rowOf(_ int, r endpointRow) feature.RowOf {
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
	row := feature.RowOf{"key": r.name, "use": use, "name": r.name, "kind": k.label, "model": model, "apiKey": key}
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
	tk := e.c.Ticket(&e.seq, feature.TicketSession)
	e.c.Go(func(ctx context.Context) any {
		us, err := e.c.Call(ctx, e.spec.verb+".usage", r.name, int64(7))
		if err != nil {
			return feature.AnswerFailed[string](err)
		}
		log, err := e.c.Call(ctx, e.spec.verb+".log", r.name, int64(30))
		if err != nil {
			return feature.AnswerFailed[string](err)
		}
		return feature.AnswerOf[string]{Value: usageText(r.name, feature.AsList(us), feature.AsList(log))}
	}, func(v any) {
		a := v.(feature.AnswerOf[string])
		if !tk.Current() {
			return
		}
		if a.Err() != nil {
			e.c.Set(e.src("Detail"), r.name+": "+feature.WireMessage(a.Err()))
			return
		}
		e.c.Set(e.src("Detail"), a.Value)
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
		u := feature.AsMap(x)
		fmt.Fprintf(&b, "\n  %s  %d requests · %d texts · %d tokens", feature.Str(u, "day"), feature.Num(u, "requests"), feature.Num(u, "texts"), feature.Num(u, "tokens"))
		if f := feature.Num(u, "failures"); f > 0 {
			fmt.Fprintf(&b, " · %d failed", f)
		}
		if l := feature.Num(u, "limited"); l > 0 {
			fmt.Fprintf(&b, " (%d at the usage limit)", l)
		}
	}
	if len(log) > 0 {
		b.WriteString("\nlatest calls")
	}
	for _, x := range log {
		ev := feature.AsMap(x)
		at := time.Unix(feature.Num(ev, "at"), 0).Format("01-02 15:04:05")
		fmt.Fprintf(&b, "\n  %s  %d texts · %d tokens · %d ms · %s", at, feature.Num(ev, "texts"), feature.Num(ev, "tokens"), feature.Num(ev, "millis"), feature.Str(ev, "outcome"))
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
		e.c.Notify(e.c.Trf("autodoc.notice.endpoints.removed_the", e.spec.noun, name))
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
	feature.SetField(e.c, e.src("Name"), r.name)
	feature.SetField(e.c, e.src("Base"), r.base)
	feature.SetField(e.c, e.src("Model"), r.model)
	feature.SetField(e.c, e.src("Key"), "")
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
			feature.SetField(e.c, e.src("Base"), e.spec.kinds[i].base)
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
	tk := e.c.Ticket(nil, feature.TicketSession)
	e.c.Go(func(ctx context.Context) any {
		res, err := e.c.Call(ctx, e.spec.verb+".models", args...)
		if err != nil {
			return feature.AnswerFailed[[]string](err)
		}
		var out []string
		for _, m := range feature.AsList(res) {
			if s, ok := m.(string); ok {
				out = append(out, s)
			}
		}
		return feature.AnswerOf[[]string]{Value: out}
	}, func(v any) {
		a := v.(feature.AnswerOf[[]string])
		if tk.Current() && e.spec.checked != nil {
			e.spec.checked(a.Value, a.Err())
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
			e.refuse("not saved: " + feature.WireMessage(err))
			return
		}
		e.c.Notify(e.c.Trf("autodoc.notice.endpoints.saved_the", e.spec.noun, spec["name"].(string)))
		e.load()
		e.c.SearchModelsChanged(e.spec.ranker)
	})
}

// refuse opens the form again as it was (the Save button closed it), saying why.
func (e *endpoints) refuse(why string) {
	e.c.Set(e.src("FormError"), why)
	e.c.Open(e.spec.noun + "Edit")
}
