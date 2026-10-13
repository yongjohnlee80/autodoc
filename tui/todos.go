package tui

import (
	"cmp"
	"context"
	"fmt"
	"os"
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

// todoCycle is s: the next status, every one but archived in turn: open → in progress →
// completed → deferred → open.
var todoCycle = map[string]string{"open": "in-progress", "in-progress": "completed", "completed": "deferred", "deferred": "open"}

// todoForward and todoBack are > and <: a step along open → in progress → completed. Deferred is
// off that line (s reaches it), and archived is the daemon's to move.
var (
	todoForward = map[string]string{"open": "in-progress", "in-progress": "completed"}
	todoBack    = map[string]string{"completed": "in-progress", "in-progress": "open"}
)

// todoLabelPref is the preference holding a store's label, by the store's id: AutoDoc's own name
// for it, which survives the store's move (M) and never touches its files.
const todoLabelPref = "tui.todos.label."

// todoFollow is a moved task the cursor is to follow: its store's row key, its id, the section it
// is in now, and the sections whose reads are still to land.
type todoFollow struct {
	store, id, bucket string
	waiting           map[string]bool
}

// todoStore is one root of the tree: where it is, as the todos.* verbs take it, and what the
// daemon says of it.
type todoStore struct {
	id      string // the row's key: "ws:<name>" or "root:<dir>"
	at      any    // a workspace name, or {root: …}
	sid     string // the daemon's id of the store, its label's key; "" when not known
	name    string // the derived name (storeName)
	alias   string // the user's label; "" shows the name
	ws      string // the workspace it is the store of, or ""
	path    string // the store directory, once known
	claimed bool   // the daemon writes it: ● and writes allowed
}

// title is how the store is called: its label, or its derived name, and the workspace it is the
// store of.
func (s *todoStore) title() string {
	t := cmp.Or(s.alias, s.name)
	if s.ws != "" {
		t += " · ws " + s.ws
	}
	return t
}

// labelKey is the store's label's preference: by its id, or its path while the id is not known.
func (s *todoStore) labelKey() string { return todoLabelPref + cmp.Or(s.sid, s.path) }

// storeName is a store's derived name: its project's folder. A KB folder (kb, alone or in
// .auto-agents or .auto-agents-config) is named for the project that holds it, the first folder
// above it that is none of those: …/LabelManager/.auto-agents/kb is LabelManager.
func storeName(root string) string {
	dir := filepath.Clean(root)
	if filepath.Base(dir) != "kb" {
		return filepath.Base(dir)
	}
	for d := filepath.Dir(dir); filepath.Dir(d) != d; d = filepath.Dir(d) {
		switch b := filepath.Base(d); b {
		case "kb", ".auto-agents", ".auto-agents-config":
		default:
			return b
		}
	}
	return filepath.Base(dir)
}

// tildePath is path with the home directory as ~, as the panel shows a store's place.
func tildePath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// storeRoot is the project root of a store directory: the folder holding its .todo-list.
func storeRoot(path string) string {
	if filepath.Base(path) == ".todo-list" {
		return filepath.Dir(path)
	}
	return path
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
	// gen is the tree's generation, one more each time the stores are listed: a read asked of an
	// older tree is dropped, its rows not this tree's
	gen      uint64
	folderAs string // what the folder chosen is for: "load", "new" or "move"
	// labelling is the store L is labelling, while its prompt is open
	labelling *todoStore
	// follow is the task a move (<, >, s) sent to another section: the cursor goes to it there once
	// the reads of both sections, the one it left and the one it is in, have landed, in either
	// order (a section that changes after the cursor is placed moves the rows under it)
	follow todoFollow
	detail bool // the details show (i, o)
	help   bool // the keys show (?)
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
		"App.todoFolder": "", "App.todoTree": t.model, "App.todoLabelText": "",
		"App.todoCanPreview": false, "App.todoCanAdd": false, "App.todoCanBack": false,
		"App.todoCanForward": false, "App.todoCanRemove": false,
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
		"App.todoForward":      none(func() { t.step(todoForward, "forward") }),
		"App.todoBack":         none(func() { t.step(todoBack, "back") }),
		"App.todoLabel":        none(t.label),
		"App.todoLabelSet":     oneString("a label", t.labelSet),
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
		builtin("todos.label_store", "label the todo store", "App.todoLabel()", t.label),
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

// Keys are autofinder's todos panel's, live while the keyboard is in the task list or its buttons,
// not in the details pane (a read-only Editor that lets the edit keys it refuses bubble): Enter is
// the tree's own (activated). 1–5 put the cursor on that section of its store.
func (*todos) Keys() []Key {
	keys := []Key{
		{Scope: "todos", Seq: "a", Show: "a", Run: "App.todoAdd()"},
		{Scope: "todos", Seq: "d", Show: "d", Run: "App.todoRemove()"},
		{Scope: "todos", Seq: "s", Show: "s", Run: "App.todoCycle()"},
		{Scope: "todos", Seq: "<", Show: "<", Run: "App.todoBack()"},
		{Scope: "todos", Seq: ">", Show: ">", Run: "App.todoForward()"},
		{Scope: "todos", Seq: "i", Show: "i", Run: "App.todoToggleDetail()"},
		{Scope: "todos", Seq: "o", Show: "o", Run: "App.todoToggleDetail()"},
		{Scope: "todos", Seq: "Shift+R", Show: "R", Run: "App.todoRefresh()"},
		{Scope: "todos", Seq: "Shift+M", Show: "M", Run: "App.todoMove()"},
		{Scope: "todos", Seq: "Shift+L", Show: "L", Run: "App.todoLabel()"},
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
// daemon knows by a root of its own; each shows with its buckets, all closed, called by its label
// or its derived name.
func (t *todos) list() {
	t.call("todos.stores", nil, &t.rootsSeq, func(v any) {
		var stores []*todoStore
		ids := map[string]string{} // store path → the daemon's id
		for _, x := range asList(v) {
			m := asMap(x)
			ids[str(m, "path")] = str(m, "id")
			roots := asList(m["roots"])
			if len(roots) == 0 {
				continue // a store with no project root is not one the verbs can reach
			}
			root, _ := roots[0].(string)
			stores = append(stores, &todoStore{id: "root:" + root, at: map[string]any{"root": root}, sid: str(m, "id"),
				name: storeName(root), path: str(m, "path"), claimed: m["claimed"] == true})
		}
		t.labels(func(labels map[string]string) {
			for _, s := range stores {
				s.alias = labels[s.labelKey()]
			}
			ws := t.c.Workspace()
			if ws == "" {
				t.show(stores)
				return
			}
			t.call("todos.resolve", []any{ws}, &t.rootsSeq, func(v any) {
				r := asMap(v)
				path := str(r, "store")
				w := &todoStore{id: "ws:" + ws, at: ws, ws: ws, sid: ids[path], name: storeName(storeRoot(path)), path: path,
					claimed: r["claimed"] == true}
				if path == "" {
					w.name = ws
				}
				w.alias = labels[w.labelKey()]
				// the workspace's store listed by its root as well shows once, as the workspace's
				stores = slices.DeleteFunc(stores, func(s *todoStore) bool { return w.path != "" && s.path == w.path })
				t.show(append([]*todoStore{w}, stores...))
			})
		})
	})
}

// labels reads the stores' labels, by their preference: none when the daemon keeps no
// preferences, so the stores still list, by their derived names.
func (t *todos) labels(then func(map[string]string)) {
	tk := t.c.Ticket(&t.rootsSeq, TicketSession)
	t.c.Go(func(ctx context.Context) any {
		v, err := t.c.Call(ctx, "preference.list")
		if err != nil {
			return map[string]any{}
		}
		return asMap(v)
	}, func(v any) {
		if !tk.Current() {
			return
		}
		labels := map[string]string{}
		for k, x := range v.(map[string]any) {
			if l, ok := x.(string); ok && strings.HasPrefix(k, todoLabelPref) && l != "" {
				labels[k] = l
			}
		}
		then(labels)
	})
}

// show puts stores in the tree, each with its buckets, closed: a section is read when it opens.
func (t *todos) show(stores []*todoStore) {
	t.gen++
	t.stores, t.open, t.tasks = stores, map[string]bool{}, map[string]map[string]any{}
	t.follow = todoFollow{} // a move asked of the old tree follows nothing in the new
	var rows []tuidecl.TreeRow
	for _, s := range stores {
		mark := "  "
		if s.claimed {
			mark = "● "
		}
		label := mark + s.title()
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
	gen := t.gen
	t.call("todos.list", []any{s.at, map[string]any{"buckets": []any{bucket}}}, t.seq(key), func(v any) {
		ix, ok := t.indexOf(path...)
		if gen != t.gen || !ok {
			return // asked of a tree since listed again
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
			// the months open before are new rows now, empty: each is read again under its new row
			for k := range t.open {
				if kind, id, month := splitTodoKey(k); kind == "m" && id == s.id {
					if _, still := t.indexOf(s.id, key, k); still {
						t.read(s, "m", month)
					} else {
						delete(t.open, k) // the month is gone
					}
				}
			}
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
		t.landed(key)
		msg := fmt.Sprintf("%d tasks", len(tasks))
		if bad := len(asList(l["malformed"])); bad > 0 {
			msg += fmt.Sprintf(" · %d malformed files", bad)
		}
		t.status(msg)
		t.showDetail()
	})
}

// reread reads again what of store s was open: its sections, and through its Archived section the
// months open in it, once their new rows are there.
func (t *todos) reread(s *todoStore) {
	for key := range t.open {
		kind, id, rest := splitTodoKey(key)
		if id == s.id && kind == "b" {
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
	t.c.Set("App.todosStatus", s+"   ? keys") // the buttons name their own keys; ? lists them all
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

// showDetail shows the details of the row under the cursor (a task's, as autofinder's i does; a
// store's, where it is and what it is called), the store's place as the heading, and which of the
// task buttons apply. It runs when the cursor moves and when an answer lands, never inside a
// button's own click, so no button disables itself (a golib Button's handler must not).
func (t *todos) showDetail() {
	s, x := t.cursor()
	heading := "todos"
	if s != nil && s.path != "" {
		heading += " · " + tildePath(s.path)
	}
	t.c.Set("App.todosTitle", heading)
	switch {
	case x != nil:
		t.c.Set("App.todoDetail", todoDetail(x))
	case s != nil && len(t.at) == 1:
		t.c.Set("App.todoDetail", storeDetail(s))
	default:
		t.c.Set("App.todoDetail", "")
	}
	// Posted: the cursor's move reaches here from the tree's currentIndexChanged, a Bus delivery,
	// and a button disabled there could not end a gesture it held (golib's Bus subscribers enqueue
	// an Update for that). Read when it runs, so the cursor is where it is then.
	t.c.Post(t.syncButtons)
}

// syncButtons sets which task buttons apply to the row under the cursor, as their keys refuse the
// others: a task for Preview, Remove and the steps; a store taking writes here for Add and the
// writes; a step only where the task's status has one.
func (t *todos) syncButtons() {
	s, x := t.cursor()
	writable := s != nil && s.claimed
	status := str(x, "status")
	_, back := todoBack[status]
	_, forward := todoForward[status]
	t.c.Set("App.todoCanPreview", x != nil)
	t.c.Set("App.todoCanAdd", writable)
	t.c.Set("App.todoCanBack", x != nil && writable && back)
	t.c.Set("App.todoCanForward", x != nil && writable && forward)
	t.c.Set("App.todoCanRemove", x != nil && writable)
}

// storeDetail is a store's details, Markdown: what it is called, where it is, who writes it.
func storeDetail(s *todoStore) string {
	var b strings.Builder
	b.WriteString("## " + s.title() + "\n\n")
	fmt.Fprintf(&b, "- folder: %s\n", tildePath(cmp.Or(s.path, rootOf(s.at))))
	fmt.Fprintf(&b, "- name: %s\n", s.name)
	if s.alias != "" {
		fmt.Fprintf(&b, "- label: %s (L to change it; empty is the name)\n", s.alias)
	} else {
		b.WriteString("- label: none (L gives it one)\n")
	}
	if s.claimed {
		b.WriteString("- writes: here (●)\n")
	} else {
		b.WriteString("- writes: read-only until the cutover\n")
	}
	return b.String()
}

// todoDetail is a task's details, as autofinder's i shows them.
func todoDetail(x map[string]any) string {
	var b strings.Builder
	b.WriteString("## " + str(x, "title") + "\n\n")
	for _, f := range []string{"status", "priority", "assignee", "due", "created", "updated"} {
		if v := str(x, f); v != "" {
			fmt.Fprintf(&b, "- %s: %s\n", f, v)
		}
	}
	for _, f := range []string{"tags", "adr", "review", "blocked"} {
		for _, v := range asList(x[f]) {
			fmt.Fprintf(&b, "- %s: %v\n", f, v)
		}
	}
	if errs := asList(x["errors"]); len(errs) > 0 {
		b.WriteString("\nerrors:\n\n")
		for _, e := range errs {
			m := asMap(e)
			fmt.Fprintf(&b, "- %s %s: %s\n", str(m, "field"), str(m, "code"), str(m, "message"))
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

// cycle is s: open → in progress → completed → deferred → open.
func (t *todos) cycle() {
	s, x := t.cursor()
	if x == nil {
		return
	}
	next, ok := todoCycle[str(x, "status")]
	if !ok {
		return // archived: Status from the daemon's verbs, not this key
	}
	t.moveTo(s, x, next)
}

// step is > (forward) and <: the task a step along open → in progress → completed. At an end, on a
// deferred or an archived task, nothing moves, and the status line says why.
func (t *todos) step(steps map[string]string, way string) {
	s, x := t.cursor()
	if x == nil {
		return
	}
	next, ok := steps[str(x, "status")]
	if !ok {
		t.status(fmt.Sprintf("%s: nothing %s of %s", str(x, "title"), way, str(x, "status")))
		return
	}
	t.moveTo(s, x, next)
}

// moveTo gives task x of store s its next status, through todos.status so its events fire; the
// cursor follows the task into its new section once the daemon has moved it.
func (t *todos) moveTo(s *todoStore, x map[string]any, next string) {
	from, to := bucketKey(s.id, str(x, "status")), bucketKey(s.id, next)
	t.write(s, "todos.status", []any{s.at, str(x, "id"), str(x, "rev"), next}, func(any) {
		t.follow = todoFollow{store: s.id, id: str(x, "id"), bucket: next, waiting: map[string]bool{from: true, to: true}}
		t.open[to] = true // the write's reread reads it, open or not, with the section the task left
	})
}

// landed is section key's read applied: once the moved task's two sections have both landed, the
// cursor goes to the task where it is now.
func (t *todos) landed(key string) {
	f := &t.follow
	if f.store == "" || !f.waiting[key] {
		return
	}
	delete(f.waiting, key)
	if len(f.waiting) > 0 {
		return
	}
	row, ok := t.indexOf(f.store, bucketKey(f.store, f.bucket), taskKey(f.store, f.id))
	f.store = ""
	if !ok {
		return
	}
	t.at = t.keysOf(row)
	if err := t.c.View(todosPanelSpec().View, "setCurrentIndex", row); err != nil {
		t.c.Failed("todos: the moved task", err)
	}
}

// label is L: the store under the cursor's label asked for (todoLabel), its own filled in. A label
// is AutoDoc's alone, so a read-only store takes one too.
func (t *todos) label() {
	s, _ := t.cursor()
	if s == nil {
		return
	}
	t.labelling = s
	t.c.Set("App.todoLabelText", s.alias)
	t.c.Open("todoLabel")
}

// labelSet keeps the label typed for the store L asked about; an empty one is the derived name.
func (t *todos) labelSet(text string) {
	t.c.Close("todoLabel")
	s := t.labelling
	t.labelling = nil
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	t.call("preference.set", []any{s.labelKey(), text}, nil, func(any) {
		if text == "" {
			t.c.Notify(fmt.Sprintf("todos: %s shows as %s again", tildePath(s.path), s.name))
		} else {
			t.c.Notify(fmt.Sprintf("todos: %s is labelled %s", tildePath(s.path), text))
		}
		t.list()
	})
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
				s := &todoStore{id: "root:" + dir, at: at, name: storeName(dir), claimed: asMap(v)["claimed"] == true}
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
