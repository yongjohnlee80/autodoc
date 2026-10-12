package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// TODOS — the todo stores as AutoDoc's own view (ADR 1791532020 §2.7): Todos › Open todos… or
// SPC x, a panel in the one dock style, at the right edge unless moved. A tree, as autofinder's
// todos panel is: the stores open, ● on those the daemon writes; under each its buckets (Open, In
// progress, Deferred, Completed, Archived); under a bucket its tasks, read when the bucket first
// opens, errored ones first and marked ⚠, the Open bucket's numbered; Archived by month, a month's
// tasks read when it opens. The keys are autofinder's, live while the keyboard is in the panel.
// Every read and write is a todos.* verb: the daemon owns the format and the rules, so this file
// only shows and asks. A store the daemon has not claimed is read-only here until the cutover.

// todoBuckets are a store's sections, in order: each one's bucket, and its title.
var todoBuckets = []struct{ bucket, title string }{
	{"open", "Open"}, {"in-progress", "In progress"}, {"deferred", "Deferred"}, {"completed", "Completed"}, {"archived", "Archived"},
}

// todoCycle is s: the next status, as autofinder's todos panel cycles it.
var todoCycle = map[string]string{"open": "completed", "completed": "deferred", "deferred": "open", "in-progress": "completed"}

// todoStore is one root of the tree: where it is, as the todos.* verbs take it, and what the
// daemon says of it.
type todoStore struct {
	id      string // the row's key: "ws:<name>" or "root:<dir>"
	at      any    // a workspace name, or {root: …}
	label   string
	path    string // the store directory, once known
	claimed bool   // the daemon writes it: ● and writes allowed
}

// todos is the feature.
type todos struct {
	c     Core
	model *tuidecl.TreeListModel
	// stores are the roots, in the order the tree shows them
	stores []*todoStore
	// tasks are the task rows' tasks, by row key
	tasks map[string]map[string]any
	// at is the row under the cursor, by its keys from the root down
	at []string
	// open are the sections and months read since the stores were last listed: a change reads
	// them again
	open map[string]bool
	// rootsSeq is the latest listing of the stores asked for, seqs each section's latest read: an
	// older answer is dropped; a write's answer never is
	rootsSeq uint64
	seqs     map[string]*uint64
	folderAs string // what the folder chosen is for: "load", "new" or "move"
	detail   bool   // the details show (i, o)
	help     bool   // the keys show (?)
}

func newTodos(c Core) *todos {
	t := &todos{c: c, detail: true, model: tuidecl.NewTreeListModel("key", "label"),
		tasks: map[string]map[string]any{}, open: map[string]bool{}, seqs: map[string]*uint64{}}
	t.model.OnFetch = t.fetch
	return t
}

func (*todos) Name() string { return "todos" }

func (t *todos) State() map[string]any {
	return map[string]any{
		"App.todosTitle": "todos", "App.todosStatus": "", "App.todoDetail": "",
		"App.todoDetailShown": true, "App.todoHelpShown": false,
		"App.todoFolder": "", "App.todoTree": t.model,
	}
}

func (t *todos) Commands() map[string]Command {
	return map[string]Command{
		"App.openTodos":        none(t.toggle),
		"App.todosTab":         oneNumber("a section", t.section),
		"App.todoMoved":        oneIndex(t.moved),
		"App.todoActivated":    oneIndex(t.activated),
		"App.todoAdd":          none(t.add),
		"App.todoAddTitle":     oneString("a title", t.addTitle),
		"App.todoRemove":       none(t.remove),
		"App.todoCycle":        none(t.cycle),
		"App.todoRefresh":      none(t.refresh),
		"App.todoToggleDetail": none(t.toggleDetail),
		"App.todoToggleHelp":   none(t.toggleHelp),
		"App.todoLoad":         none(t.chooseLoad),
		"App.todoNew":          none(t.chooseNew),
		"App.todoMove":         none(func() { t.chooseFolder("move", "move the store: its new folder") }),
		"App.todoFolderChosen": oneString("a folder", t.folderChosen),
	}
}

// Catalog is the todos' user commands (catalog.go): each runs what its App.* twin runs.
func (t *todos) Catalog() []Entry {
	return []Entry{
		{ID: "todos.open", Title: "todos", Verb: "todos", Help: "the todos panel, open or closed (SPC x)", Twin: "App.openTodos()",
			Run: func(Invocation) error { t.toggle(); return nil }},
		builtin("todos.new_store", "a new todo store", "App.todoNew()", t.chooseNew),
		builtin("todos.load_store", "load a todo store", "App.todoLoad()", t.chooseLoad),
	}
}

// todosPanelSpec is the todos panel: at the right edge by default, where the explorer's left one
// leaves room, its geometry kept per edge like the side panels'.
func todosPanelSpec() PanelSpec {
	return sidePanelSpec("todos", "todoTree", 60, func(p prefs) string { return p.todosEdge })
}

// Panels: the panel, its stores listed afresh each time it opens.
func (t *todos) Panels() []PanelSpec {
	s := todosPanelSpec()
	s.Opened = func(PanelHost) { t.list() }
	return []PanelSpec{s}
}

// Keys are autofinder's todos panel's, live while the keyboard is in the panel: Enter is the tree's
// own (activated). 1–5 put the cursor on that section of its store.
func (*todos) Keys() []Key {
	keys := []Key{
		{Scope: "todos", Seq: "a", Show: "a", Run: "App.todoAdd()"},
		{Scope: "todos", Seq: "d", Show: "d", Run: "App.todoRemove()"},
		{Scope: "todos", Seq: "s", Show: "s", Run: "App.todoCycle()"},
		{Scope: "todos", Seq: "i", Show: "i", Run: "App.todoToggleDetail()"},
		{Scope: "todos", Seq: "o", Show: "o", Run: "App.todoToggleDetail()"},
		{Scope: "todos", Seq: "Shift+R", Show: "R", Run: "App.todoRefresh()"},
		{Scope: "todos", Seq: "Shift+M", Show: "M", Run: "App.todoMove()"},
		{Scope: "todos", Seq: "?", Show: "?", Run: "App.todoToggleHelp()"},
	}
	for i := range todoBuckets {
		keys = append(keys, Key{Scope: "todos", Seq: fmt.Sprint(i + 1), Show: "1–5", Run: fmt.Sprintf("App.todosTab(%d)", i)})
	}
	return keys
}

// Cards: the todos have no card of keys.
func (*todos) Cards() []Card { return nil }

func (t *todos) chooseLoad() { t.chooseFolder("load", "load a todo store: its project's folder") }
func (t *todos) chooseNew()  { t.chooseFolder("new", "a new todo store: the project's folder") }

// Kinds: another client's change to a store shows here when its store is listed.
func (t *todos) Kinds() []string { return []string{"todos.changed"} }

func (t *todos) Event(_, workspace, detail string) {
	for _, s := range t.stores {
		ws, isWorkspace := s.at.(string)
		root := rootOf(s.at)
		if isWorkspace && ws == workspace || root != "" && (detail == root || strings.HasPrefix(detail, root+"#")) {
			t.reread(s)
		}
	}
}

// Entered: the workspace entered is the first store listed, the next time the stores are.
func (t *todos) Entered(string) {}

func rootOf(at any) string {
	if m, ok := at.(map[string]any); ok {
		r, _ := m["root"].(string)
		return r
	}
	return ""
}

// toggle is Todos › Open todos… (SPC x): the panel, open or closed.
func (t *todos) toggle() {
	if t.c.Workspace() == "" && len(t.stores) == 0 {
		t.c.Notify("todos: enter a workspace first, or Load a store")
		return
	}
	t.c.TogglePanel(todosPanelSpec().ID)
}

// call runs a todos verb off the loop and hands its answer to then on the loop. With a sequence,
// only the latest call of that sequence is answered, its error as well as its result; without
// one (a write), every call is.
func (t *todos) call(verb string, params []any, seq *uint64, then func(any)) {
	tk := Ticket{}
	if seq != nil {
		tk = t.c.Ticket(seq, TicketSession)
	}
	t.c.Go(func(ctx context.Context) any {
		v, err := t.c.Call(ctx, verb, params...)
		if err != nil {
			return err
		}
		return answer{v}
	}, func(v any) {
		if !tk.Current() {
			return
		}
		if err, ok := v.(error); ok {
			t.c.Failed(verb, err)
			return
		}
		then(v.(answer).v)
	})
}

// answer wraps a verb's result, so a nil one is not taken for an error.
type answer struct{ v any }

// list reads the stores: the workspace's first, as todos.resolve finds it, then every other the
// daemon knows by a root of its own; each shows with its buckets, all closed.
func (t *todos) list() {
	t.call("todos.stores", nil, &t.rootsSeq, func(v any) {
		var stores []*todoStore
		for _, x := range asList(v) {
			m := asMap(x)
			roots := asList(m["roots"])
			if len(roots) == 0 {
				continue // a store with no project root is not one the verbs can reach
			}
			root, _ := roots[0].(string)
			stores = append(stores, &todoStore{id: "root:" + root, at: map[string]any{"root": root}, label: root,
				path: str(m, "path"), claimed: m["claimed"] == true})
		}
		ws := t.c.Workspace()
		if ws == "" {
			t.show(stores)
			return
		}
		t.call("todos.resolve", []any{ws}, &t.rootsSeq, func(v any) {
			r := asMap(v)
			w := &todoStore{id: "ws:" + ws, at: ws, label: ws, path: str(r, "store"), claimed: r["claimed"] == true}
			// the workspace's store listed by its root as well shows once, as the workspace's
			stores = slices.DeleteFunc(stores, func(s *todoStore) bool { return w.path != "" && s.path == w.path })
			t.show(append([]*todoStore{w}, stores...))
		})
	})
}

// show puts stores in the tree, each with its buckets, closed: a section is read when it opens.
func (t *todos) show(stores []*todoStore) {
	t.stores, t.open, t.tasks = stores, map[string]bool{}, map[string]map[string]any{}
	var rows []tuidecl.TreeRow
	for _, s := range stores {
		mark := "  "
		if s.claimed {
			mark = "● "
		}
		label := mark + s.label
		if !s.claimed {
			label += "  (read-only until the cutover)"
		}
		rows = append(rows, tuidecl.TreeRow{Row: rowOf{"key": s.id, "label": label}, HasChildren: true})
	}
	t.model.SetChildren(nil, rows)
	for i, s := range stores {
		ix := tuidecl.Index{Row: i}
		var buckets []tuidecl.TreeRow
		for _, b := range todoBuckets {
			buckets = append(buckets, tuidecl.TreeRow{Row: rowOf{"key": bucketKey(s.id, b.bucket), "label": b.title}, HasChildren: true})
		}
		t.model.SetChildren(&ix, buckets)
	}
	t.at = nil
	t.status(fmt.Sprintf("%d stores: open a section for its tasks", len(stores)))
	t.showDetail()
}

func bucketKey(store, bucket string) string { return "b\x00" + store + "\x00" + bucket }
func monthKey(store, month string) string   { return "m\x00" + store + "\x00" + month }
func taskKey(store, id string) string       { return "t\x00" + store + "\x00" + id }

// splitTodoKey is a row key's kind, its store, and the rest: a bucket, a month or a task's id.
func splitTodoKey(k string) (kind, store, rest string) {
	parts := strings.SplitN(k, "\x00", 3)
	if len(parts) != 3 {
		return "store", k, ""
	}
	return parts[0], parts[1], parts[2]
}

// seq is the latest-read sequence of a row's reads.
func (t *todos) seq(key string) *uint64 {
	if t.seqs[key] == nil {
		t.seqs[key] = new(uint64)
	}
	return t.seqs[key]
}

// store is the store of id.
func (t *todos) store(id string) *todoStore {
	if i := slices.IndexFunc(t.stores, func(s *todoStore) bool { return s.id == id }); i >= 0 {
		return t.stores[i]
	}
	return nil
}

// fetch is a section or a month opened for the first time: its rows read.
func (t *todos) fetch(ix tuidecl.Index) {
	kind, id, rest := splitTodoKey(t.model.Key(ix))
	if s := t.store(id); s != nil && (kind == "b" || kind == "m") {
		t.read(s, kind, rest)
	}
}

// read lists a section ("b", a bucket) or an archived month ("m") of store s, and puts its rows
// under it: wherever its row is when the answer comes.
func (t *todos) read(s *todoStore, kind, rest string) {
	key, bucket := bucketKey(s.id, rest), rest
	path := []string{s.id, key} // a section is a store's row; a month, its Archived section's
	if kind == "m" {
		key, bucket = monthKey(s.id, rest), "archived/"+rest
		path = []string{s.id, bucketKey(s.id, "archived"), key}
	}
	t.open[key] = true
	t.call("todos.list", []any{s.at, map[string]any{"buckets": []any{bucket}}}, t.seq(key), func(v any) {
		ix, ok := t.indexOf(path...)
		if !ok {
			return // the stores were listed again since
		}
		l := asMap(v)
		if kind == "b" && rest == "archived" {
			var months []tuidecl.TreeRow
			for _, p := range asList(l["partitions"]) {
				m := asMap(p)
				months = append(months, tuidecl.TreeRow{Row: rowOf{"key": monthKey(s.id, str(m, "month")),
					"label": fmt.Sprintf("%s  (%v)", str(m, "month"), m["files"])}, HasChildren: true})
			}
			t.model.SetChildren(&ix, months)
			t.status(fmt.Sprintf("%d months archived: open one for its tasks", len(months)))
			return
		}
		tasks := asList(l["tasks"])
		var errored, clean []map[string]any
		for _, x := range tasks {
			m := asMap(x)
			if len(asList(m["errors"])) > 0 {
				errored = append(errored, m)
			} else {
				clean = append(clean, m)
			}
		}
		var rows []tuidecl.TreeRow
		for i, m := range append(errored, clean...) {
			k := taskKey(s.id, str(m, "id"))
			t.tasks[k] = m
			label := str(m, "title")
			if kind == "b" && rest == "open" {
				label = fmt.Sprintf("%d  %s", i+1, label) // the open bucket is numbered, as autofinder's is
			}
			if len(asList(m["errors"])) > 0 {
				label = "⚠ " + label
			}
			rows = append(rows, tuidecl.TreeRow{Row: rowOf{"key": k, "label": label}})
		}
		t.model.SetChildren(&ix, rows)
		msg := fmt.Sprintf("%d tasks", len(tasks))
		if bad := len(asList(l["malformed"])); bad > 0 {
			msg += fmt.Sprintf(" · %d malformed files", bad)
		}
		t.status(msg)
		t.showDetail()
	})
}

// reread reads again what of store s was open: its sections and months.
func (t *todos) reread(s *todoStore) {
	for key := range t.open {
		kind, id, rest := splitTodoKey(key)
		if id == s.id {
			t.read(s, kind, rest)
		}
	}
}

// indexOf is the row of keys, from the root down.
func (t *todos) indexOf(keys ...string) (tuidecl.Index, bool) {
	var parent *tuidecl.Index
	var ix tuidecl.Index
	for _, k := range keys {
		found := false
		for r := 0; r < t.model.RowCount(parent); r++ {
			at := tuidecl.Index{Row: r, Parent: parent}
			if t.model.Key(at) == k {
				ix, found = at, true
				break
			}
		}
		if !found {
			return tuidecl.Index{}, false
		}
		p := ix
		parent = &p
	}
	return ix, len(keys) > 0
}

func (t *todos) status(s string) {
	t.c.Set("App.todosStatus", s+"   a add · s status · d remove · R refresh · i details · M move · ? keys")
}

// keysOf is a row's identity: the keys from the root down to it.
func (t *todos) keysOf(ix tuidecl.Index) []string {
	var keys []string
	for p := &ix; p != nil; p = p.Parent {
		keys = append([]string{t.model.Key(*p)}, keys...)
	}
	return keys
}

// moved is the cursor moving: the row under it kept, its task's details shown.
func (t *todos) moved(ix tuidecl.Index) error {
	t.at = t.keysOf(ix)
	t.showDetail()
	return nil
}

// cursor is the row under the cursor: its store, and its task when it is one.
func (t *todos) cursor() (*todoStore, map[string]any) {
	if len(t.at) == 0 {
		if len(t.stores) > 0 {
			return t.stores[0], nil
		}
		return nil, nil
	}
	last := t.at[len(t.at)-1]
	_, id, _ := splitTodoKey(last)
	if kind, _, _ := splitTodoKey(last); kind == "store" {
		id = last
	}
	return t.store(id), t.tasks[last]
}

// showDetail shows the task under the cursor's details, as autofinder's i does.
func (t *todos) showDetail() {
	_, x := t.cursor()
	if x == nil {
		t.c.Set("App.todoDetail", "")
		return
	}
	t.c.Set("App.todoDetail", todoDetail(x))
}

// todoDetail is a task's details, as autofinder's i shows them.
func todoDetail(x map[string]any) string {
	var b strings.Builder
	b.WriteString(str(x, "title") + "\n\n")
	for _, f := range []string{"status", "priority", "assignee", "due", "created", "updated"} {
		if v := str(x, f); v != "" {
			fmt.Fprintf(&b, "%-9s %s\n", f+":", v)
		}
	}
	for _, f := range []string{"tags", "adr", "review", "blocked"} {
		for _, v := range asList(x[f]) {
			fmt.Fprintf(&b, "%-9s %v\n", f+":", v)
		}
	}
	if errs := asList(x["errors"]); len(errs) > 0 {
		b.WriteString("\nerrors:\n")
		for _, e := range errs {
			m := asMap(e)
			fmt.Fprintf(&b, "  %s %s: %s\n", str(m, "field"), str(m, "code"), str(m, "message"))
		}
	}
	if d := strings.TrimSpace(str(x, "description")); d != "" {
		b.WriteString("\n" + d + "\n")
	}
	return b.String()
}

// activated is Enter: a task's file opens in the editor, the panel closing; a store, a section or a
// month opens or closes.
func (t *todos) activated(ix tuidecl.Index) error {
	key := t.model.Key(ix)
	kind, id, _ := splitTodoKey(key)
	if kind != "t" {
		return t.c.View(todosPanelSpec().View, "toggleExpanded", ix)
	}
	s, x := t.store(id), t.tasks[key]
	if s == nil || x == nil || s.path == "" {
		return nil
	}
	err := t.c.View(todosPanelSpec().ID, "close")
	t.c.OpenFile(filepath.Join(s.path, filepath.FromSlash(str(x, "path"))))
	return err
}

// section is 1–5: the cursor to that section of the store it is in, the first store's when it is
// in none.
func (t *todos) section(i int) {
	s, _ := t.cursor()
	if s == nil || i < 0 || i >= len(todoBuckets) {
		return
	}
	ix, ok := t.indexOf(s.id, bucketKey(s.id, todoBuckets[i].bucket))
	if !ok {
		return
	}
	t.at = t.keysOf(ix)
	if err := t.c.View(todosPanelSpec().View, "setCurrentIndex", ix); err != nil {
		t.c.Failed("todos: a section", err)
	}
}

// readOnly reports, and says, that store s takes no writes here: the daemon has not claimed it.
func (t *todos) readOnly(s *todoStore) bool {
	if s == nil {
		return true
	}
	if !s.claimed {
		t.c.Notify("todos: this store is read-only in AutoDoc until the cutover; change it through AutoVim")
	}
	return !s.claimed
}

// write runs a todos verb that changes store s, once s takes writes here, and reads it again.
func (t *todos) write(s *todoStore, verb string, params []any, done func(any)) {
	if t.readOnly(s) {
		return
	}
	t.call(verb, params, nil, func(v any) {
		if done != nil {
			done(v)
		}
		t.reread(s)
	})
}

// add is a: the title of a new task in the cursor's store asked for (todoNew).
func (t *todos) add() {
	s, _ := t.cursor()
	if !t.readOnly(s) {
		t.c.Open("todoNew")
	}
}

func (t *todos) addTitle(title string) {
	t.c.Close("todoNew")
	s, _ := t.cursor()
	if s == nil || strings.TrimSpace(title) == "" {
		return
	}
	t.write(s, "todos.add", []any{s.at, map[string]any{"title": strings.TrimSpace(title)}}, func(v any) {
		t.c.Notify("todos: added " + str(asMap(v), "id"))
	})
}

func (t *todos) remove() {
	s, x := t.cursor()
	if x == nil || t.readOnly(s) {
		return
	}
	t.c.Ask(Question{Title: "remove the task?", Text: fmt.Sprintf("Remove %q? Its file is deleted.", str(x, "title")),
		OnYes: func() { t.removeConfirmed(s, x) }})
}

// removeConfirmed removes the task asked about, whatever row the cursor is on by then.
func (t *todos) removeConfirmed(s *todoStore, x map[string]any) {
	t.write(s, "todos.remove", []any{s.at, str(x, "id"), str(x, "rev")}, func(any) { t.c.Notify("todos: removed " + str(x, "id")) })
}

// cycle is s: open → completed → deferred → open.
func (t *todos) cycle() {
	s, x := t.cursor()
	if x == nil {
		return
	}
	next, ok := todoCycle[str(x, "status")]
	if !ok {
		return // archived: Status from the daemon's verbs, not this key
	}
	t.write(s, "todos.status", []any{s.at, str(x, "id"), str(x, "rev"), next}, nil)
}

// refresh is R: the daemon reconciles the store's active buckets, then what was open is read again.
func (t *todos) refresh() {
	s, _ := t.cursor()
	if s == nil {
		return
	}
	if !s.claimed {
		t.reread(s) // read-only: read again, nothing to reconcile
		return
	}
	t.write(s, "todos.refresh", []any{s.at}, func(v any) {
		r := asMap(v)
		t.c.Notify(fmt.Sprintf("todos: refreshed: %v moved, %v archived, %v rewritten", r["moved"], r["archived"], r["rewritten"]))
	})
}

func (t *todos) toggleDetail() { t.detail = !t.detail; t.c.Set("App.todoDetailShown", t.detail) }
func (t *todos) toggleHelp()   { t.help = !t.help; t.c.Set("App.todoHelpShown", t.help) }

// chooseFolder opens the folder dialog for Load, New or M (move).
func (t *todos) chooseFolder(as, title string) {
	s, _ := t.cursor()
	if as == "move" && t.readOnly(s) {
		return
	}
	t.folderAs = as
	t.status(title)
	start := ""
	if s != nil {
		start = rootOf(s.at)
		if start == "" && s.path != "" {
			start = filepath.Dir(s.path)
		}
	}
	t.c.Set("App.todoFolder", start)
	t.c.Open("todoFolder")
}

// folderChosen acts on the folder: Load registers the project and lists its store; New registers it
// and creates its store (a write: after the cutover); M moves the cursor's store there.
func (t *todos) folderChosen(dir string) {
	t.c.Close("todoFolder")
	if dir == "" {
		return
	}
	dir = filepath.Clean(dir)
	if filepath.Base(dir) == ".todo-list" {
		dir = filepath.Dir(dir) // the store itself was chosen: its project is the root
	}
	at := map[string]any{"root": dir}
	switch t.folderAs {
	case "load":
		t.call("todos.register", []any{dir}, nil, func(any) { t.list() })
	case "new":
		t.call("todos.register", []any{dir}, nil, func(any) {
			t.call("todos.resolve", []any{at}, nil, func(v any) {
				s := &todoStore{id: "root:" + dir, at: at, label: dir, claimed: asMap(v)["claimed"] == true}
				t.write(s, "todos.scaffold", []any{at}, func(any) { t.c.Notify("todos: a new store in " + dir); t.list() })
			})
		})
	case "move":
		s, _ := t.cursor()
		if s == nil {
			return
		}
		t.write(s, "todos.move_store", []any{s.at, filepath.Join(dir, ".todo-list")}, func(any) {
			t.c.Notify("todos: the store is now in " + dir)
			t.list()
		})
	}
}
