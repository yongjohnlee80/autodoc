package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// TODOS — the todo stores as AutoDoc's own view (ADR 1791532020 §2.7): Todos › Open todos… or
// SPC x. The stores known on the left; on the right a tab per bucket, the Archived tab by month,
// read only when a month is opened. Every read and write is a todos.* verb: the daemon owns the
// format and the rules, so this file only shows and asks. A store the daemon has not claimed is
// read-only here until the cutover.

// todoBuckets are the tabs, in order: each one's bucket, and its title.
var todoBuckets = []struct{ bucket, title string }{
	{"open", "Open"}, {"in-progress", "In progress"}, {"deferred", "Deferred"}, {"completed", "Completed"}, {"archived", "Archived"},
}

// todoTabs is how many there are; archivedTab is the last.
const (
	todoTabs    = 5
	archivedTab = 4
)

// todoCycle is s: the next status, as autofinder's todos panel cycles it.
var todoCycle = map[string]string{"open": "completed", "completed": "deferred", "deferred": "open", "in-progress": "completed"}

// todos is the feature.
type todos struct {
	c Core

	at       any    // the store's where: a workspace name, or {root: …}; nil before one is chosen
	atLabel  string // what the title calls it
	store    string // the store directory, as todos.resolve answers
	claimed  bool
	tab      int
	month    string                          // the archived month open; "" for the month list
	tasks    [todoTabs]*rows[map[string]any] // each tab's tasks
	months   *rows[map[string]any]           // the archived months, while the Archived tab lists them
	stores   *rows[map[string]any]           // the stores the daemon knows
	cursor   int                             // the row under the cursor in the tab shown
	folderAs string                          // what the folder chosen is for: "load", "new" or "move"
	// the latest store read and the latest store list asked for: an older answer of either is
	// dropped; the two never cancel each other, and a write's answer is never dropped
	loadSeq, storesSeq uint64
	detail             bool           // the details pane shows (i, o)
	help               bool           // the keys show (?)
	loaded             [todoTabs]bool // the tab's tasks were read since it was last shown empty
}

func newTodos(c Core) *todos {
	t := &todos{c: c, detail: true,
		stores: newRows(tuidecl.NewListModel("key", "mark", "name", "path"), storeRowOf),
		months: newRows(tuidecl.NewListModel("key", "month", "count"), monthRowOf)}
	for i := range t.tasks {
		t.tasks[i] = newRows(tuidecl.NewListModel("key", "n", "title", "priority", "assignee", "due", "errors"), taskRowOf(i == 0))
	}
	return t
}

func (*todos) Name() string { return "todos" }

func (t *todos) State() map[string]any {
	return map[string]any{
		"App.todosTitle": "todos", "App.todosStatus": "", "App.todoTab": 0, "App.todoDetail": "",
		"App.todoDetailShown": true, "App.todoHelpShown": false,
		"App.todoFolder": "",
		"App.todoStores": t.stores.Model(), "App.todoMonths": t.months.Model(),
		"App.todoOpen": t.tasks[0].Model(), "App.todoInProgress": t.tasks[1].Model(), "App.todoDeferred": t.tasks[2].Model(),
		"App.todoCompleted": t.tasks[3].Model(), "App.todoArchived": t.tasks[4].Model(),
	}
}

func (t *todos) Commands() map[string]Command {
	return map[string]Command{
		"App.openTodos":        none(t.open),
		"App.todosTab":         oneNumber("a tab", t.showTab),
		"App.todoMoved":        oneNumber("a row", t.moved),
		"App.todoActivated":    oneNumber("a row", t.activated),
		"App.todoStoreChosen":  oneNumber("a row", t.storeChosen),
		"App.todoMonthChosen":  oneNumber("a row", t.monthChosen),
		"App.todoAdd":          none(t.add),
		"App.todoAddTitle":     oneString("a title", t.addTitle),
		"App.todoRemove":       none(t.remove),
		"App.todoCycle":        none(t.cycle),
		"App.todoRefresh":      none(t.refresh),
		"App.todoToggleDetail": none(t.toggleDetail),
		"App.todoToggleHelp":   none(t.toggleHelp),
		"App.todoLoad":         none(func() { t.chooseFolder("load", "load a todo store: its project's folder") }),
		"App.todoNew":          none(func() { t.chooseFolder("new", "a new todo store: the project's folder") }),
		"App.todoMove":         none(func() { t.chooseFolder("move", "move the store: its new folder") }),
		"App.todoFolderChosen": oneString("a folder", t.folderChosen),
	}
}

// Kinds: another client's change to any store shows here when its store is the one open.
func (t *todos) Kinds() []string { return []string{"todos.changed"} }

func (t *todos) Event(_, workspace, detail string) {
	if t.at == nil {
		return
	}
	ws, isWorkspace := t.at.(string)
	root := t.rootOf()
	if isWorkspace && ws == workspace || root != "" && (detail == root || strings.HasPrefix(detail, root+"#")) {
		t.load()
	}
}

// Entered: a workspace entered is the store shown, until another is chosen.
func (t *todos) Entered(ws string) {
	if _, root := t.at.(map[string]any); !root {
		t.at, t.atLabel = ws, ws
	}
}

func (t *todos) rootOf() string {
	if m, ok := t.at.(map[string]any); ok {
		r, _ := m["root"].(string)
		return r
	}
	return ""
}

// open is Todos › Open todos… (SPC x): the workspace's store, or the one chosen last.
func (t *todos) open() {
	if t.at == nil {
		ws := t.c.Workspace()
		if ws == "" {
			t.c.Notify("todos: enter a workspace first, or Load a store")
			return
		}
		t.at, t.atLabel = ws, ws
	}
	t.tab, t.month = 0, ""
	t.c.Set("App.todoTab", 0)
	t.c.Open("todosDialog")
	t.loadStores()
	t.load()
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

// readOnly reports, and says, that the store takes no writes here: the daemon has not claimed it.
func (t *todos) readOnly() bool {
	if !t.claimed {
		t.c.Notify("todos: this store is read-only in AutoDoc until the cutover; change it through AutoVim")
	}
	return !t.claimed
}

// write runs a todos verb that changes the store, once the store takes writes here.
func (t *todos) write(verb string, params []any, done func(any)) {
	if t.readOnly() {
		return
	}
	t.call(verb, params, nil, func(v any) {
		if done != nil {
			done(v)
		}
		t.load()
	})
}

// load reads the store's state and the tab shown.
func (t *todos) load() {
	t.call("todos.resolve", []any{t.at}, &t.loadSeq, func(v any) {
		r := asMap(v)
		t.store, t.claimed = str(r, "store"), r["claimed"] == true
		title := "todos · " + t.atLabel
		if !t.claimed {
			title += "  (read-only until the cutover)"
		}
		t.c.Set("App.todosTitle", title)
		t.loadTab()
	})
}

func (t *todos) loadTab() {
	bucket := todoBuckets[t.tab].bucket
	if t.tab == archivedTab && t.month != "" {
		bucket = "archived/" + t.month
	}
	t.call("todos.list", []any{t.at, map[string]any{"buckets": []any{bucket}}}, &t.loadSeq, func(v any) {
		l := asMap(v)
		if t.tab == archivedTab && t.month == "" {
			var months []map[string]any
			for _, p := range asList(l["partitions"]) {
				months = append(months, asMap(p))
			}
			t.months.Reset(months)
			t.tasks[archivedTab].Reset(nil) // the month list shows, not a month's tasks
			t.loaded[archivedTab] = false
			t.status(fmt.Sprintf("%d months archived: Enter opens one", len(months)))
			return
		}
		var tasks []map[string]any
		for _, x := range asList(l["tasks"]) {
			tasks = append(tasks, asMap(x))
		}
		t.tasks[t.tab].Reset(tasks)
		t.loaded[t.tab] = true
		bad := len(asList(l["malformed"]))
		msg := fmt.Sprintf("%d tasks", len(tasks))
		if bad > 0 {
			msg += fmt.Sprintf(" · %d malformed files", bad)
		}
		t.status(msg)
		t.moved(0)
	})
}

// taskRowOf is a bucket's task as its row; the open bucket numbers them, as autofinder's does.
func taskRowOf(numbered bool) func(int, map[string]any) rowOf {
	return func(i int, x map[string]any) rowOf {
		n := ""
		if numbered {
			n = fmt.Sprint(i + 1)
		}
		mark := ""
		if len(asList(x["errors"])) > 0 {
			mark = "!"
		}
		return rowOf{"key": str(x, "id"), "n": n, "title": str(x, "title"), "priority": str(x, "priority"),
			"assignee": str(x, "assignee"), "due": str(x, "due"), "errors": mark}
	}
}

// monthRowOf is an archived month as its row.
func monthRowOf(i int, m map[string]any) rowOf {
	return rowOf{"key": fmt.Sprint(i), "month": str(m, "month"), "count": fmt.Sprint(m["files"])}
}

// storeRowOf is a known store as its row, the open one marked.
func storeRowOf(i int, s map[string]any) rowOf {
	mark := "  "
	if s["claimed"] == true {
		mark = "● "
	}
	name := filepath.Base(filepath.Dir(str(s, "path")))
	return rowOf{"key": fmt.Sprint(i), "mark": mark, "name": name, "path": str(s, "path")}
}

func (t *todos) status(s string) {
	t.c.Set("App.todosStatus", s+"   a add · s status · d remove · R refresh · i details · M move · ? keys")
}

func (t *todos) showTab(i int) {
	if i < 0 || i >= len(todoBuckets) || i == t.tab && t.loaded[i] {
		return
	}
	t.tab, t.month = i, ""
	t.c.Set("App.todoTab", i)
	t.loadTab()
}

// task is the row i of the tab shown; nil for none.
func (t *todos) task(i int) map[string]any {
	x, _ := t.tasks[t.tab].At(i)
	return x
}

// moved shows the task under the cursor.
func (t *todos) moved(i int) {
	t.cursor = i
	x := t.task(i)
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

// activated is Enter: the task's file, in the editor.
func (t *todos) activated(i int) {
	x := t.task(i)
	if x == nil || t.store == "" {
		return
	}
	t.c.Close("todosDialog")
	t.c.OpenFile(filepath.Join(t.store, filepath.FromSlash(str(x, "path"))))
}

func (t *todos) monthChosen(i int) {
	m, ok := t.months.At(i)
	if !ok {
		return
	}
	t.month = str(m, "month")
	t.loadTab()
}

func (t *todos) add() {
	if !t.readOnly() {
		t.c.Open("todoNew")
	}
}

func (t *todos) addTitle(title string) {
	t.c.Close("todoNew")
	if strings.TrimSpace(title) == "" {
		return
	}
	t.write("todos.add", []any{t.at, map[string]any{"title": strings.TrimSpace(title)}}, func(v any) {
		t.c.Notify("todos: added " + str(asMap(v), "id"))
	})
}

func (t *todos) remove() {
	x := t.task(t.cursor)
	if x == nil {
		return
	}
	if t.readOnly() {
		return
	}
	t.c.Ask(Question{Title: "remove the task?", Text: fmt.Sprintf("Remove %q? Its file is deleted.", str(x, "title")),
		OnYes: func() { t.removeConfirmed(x) }})
}

// removeConfirmed removes the task asked about, whatever row the cursor is on by then.
func (t *todos) removeConfirmed(x map[string]any) {
	t.write("todos.remove", []any{t.at, str(x, "id"), str(x, "rev")}, func(any) { t.c.Notify("todos: removed " + str(x, "id")) })
}

// cycle is s: open → completed → deferred → open.
func (t *todos) cycle() {
	x := t.task(t.cursor)
	if x == nil {
		return
	}
	next, ok := todoCycle[str(x, "status")]
	if !ok {
		return // archived: Status from the daemon's verbs, not this key
	}
	t.write("todos.status", []any{t.at, str(x, "id"), str(x, "rev"), next}, nil)
}

// refresh is R: the daemon reconciles the active buckets, then the tab is read again.
func (t *todos) refresh() {
	if !t.claimed {
		t.load() // read-only: read again, nothing to reconcile
		return
	}
	t.write("todos.refresh", []any{t.at}, func(v any) {
		s := asMap(v)
		t.c.Notify(fmt.Sprintf("todos: refreshed: %v moved, %v archived, %v rewritten", s["moved"], s["archived"], s["rewritten"]))
	})
}

func (t *todos) toggleDetail() { t.detail = !t.detail; t.c.Set("App.todoDetailShown", t.detail) }
func (t *todos) toggleHelp()   { t.help = !t.help; t.c.Set("App.todoHelpShown", t.help) }

// storeChosen shows another store: a known one, by its first root.
func (t *todos) storeChosen(i int) {
	s, ok := t.stores.At(i)
	if !ok {
		return
	}
	roots := asList(s["roots"])
	if len(roots) == 0 {
		return
	}
	root, _ := roots[0].(string)
	t.at, t.atLabel, t.month = map[string]any{"root": root}, root, ""
	t.load()
}

// loadStores lists the stores the daemon knows; the open one is marked.
func (t *todos) loadStores() {
	t.call("todos.stores", nil, &t.storesSeq, func(v any) {
		var stores []map[string]any
		for _, x := range asList(v) {
			stores = append(stores, asMap(x))
		}
		t.stores.Reset(stores)
	})
}

// chooseFolder opens the folder dialog for Load, New or M (move).
func (t *todos) chooseFolder(as, title string) {
	if as == "move" && t.readOnly() {
		return
	}
	t.folderAs = as
	t.status(title)
	start := t.rootOf()
	if start == "" {
		start = filepath.Dir(t.store)
	}
	t.c.Set("App.todoFolder", start)
	t.c.Open("todoFolder")
}

// folderChosen acts on the folder: Load registers the project and shows its store; New registers it
// and creates its store (a write: after the cutover); M moves the store there.
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
		t.call("todos.register", []any{dir}, nil, func(any) {
			t.at, t.atLabel, t.month = at, dir, ""
			t.loadStores()
			t.load()
		})
	case "new":
		t.call("todos.register", []any{dir}, nil, func(any) {
			t.at, t.atLabel, t.month = at, dir, ""
			t.call("todos.resolve", []any{at}, nil, func(v any) {
				t.claimed = asMap(v)["claimed"] == true
				t.write("todos.scaffold", []any{at}, func(any) { t.c.Notify("todos: a new store in " + dir); t.loadStores() })
			})
		})
	case "move":
		t.write("todos.move_store", []any{t.at, filepath.Join(dir, ".todo-list")}, func(any) {
			t.c.Notify("todos: the store is now in " + dir)
			t.loadStores()
		})
	}
}
