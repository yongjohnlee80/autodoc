package tui

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/userdirs"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// TODOS — the todo stores as AutoDoc's own view (docs/decisions/todos.md#autodocs-own-todos-view):
// Todos › Open todos… or SPC x, a panel in the one dock style, at the left edge unless moved. One
// store at a time: a dropdown along the top chooses it among the stores the daemon knows (the
// workspace's first), and icons beside it make a new store, load one from a folder, rename it (its
// label) and refresh it. Under them a tab per status (Open, In progress, Deferred, Completed,
// Archived), each a numbered table of its tasks, read when the tab first shows; Archived lists its
// months, a month's tasks read when it opens. Under the tabs the task under the cursor's details,
// then one row of the actions that apply. Every read and write is a todos.* verb: the daemon owns
// the format and the rules, so this file only shows and asks. A store todos.release gave back is
// read-only here; any other is written, its first write claiming it.

// todoBuckets are the tabs, in order: each one's bucket, and its title.
var todoBuckets = []struct{ bucket, title string }{
	{"open", "Open"}, {"in-progress", "In progress"}, {"deferred", "Deferred"}, {"completed", "Completed"}, {"archived", "Archived"},
}

// archivedTab is the last tab, whose table is the open month's tasks.
const archivedTab = 4

// todoTabs are each tab's table in main.qml and the stem of its App.* sources: App.todo<stem>Rows
// is its model, App.todo<stem>Index its cursor. The Archived tab's table is its open month's tasks;
// its months are todoMonths.
var todoTabs = [5]struct{ table, stem string }{
	{"todoOpenTable", "Open"}, {"todoInProgressTable", "InProgress"}, {"todoDeferredTable", "Deferred"},
	{"todoCompletedTable", "Completed"}, {"todoArchivedTable", "Archived"},
}

// todoStatuses are every status a task can have, in the order s's picker lists them; choosing one
// is todos.status, whatever the status now (the daemon moves an archived task too).
var todoStatuses = []string{"open", "in-progress", "deferred", "completed", "archived"}

// tabOf is the tab of a status; -1 for none.
func tabOf(status string) int {
	return slices.IndexFunc(todoBuckets[:], func(b struct{ bucket, title string }) bool { return b.bucket == status })
}

// monthOf is the archived month of a task's path, "2026/10" of archived/2026/10/x.md; "" for none.
func monthOf(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 4 || parts[0] != "archived" {
		return ""
	}
	return parts[1] + "/" + parts[2]
}

// todoLabelPref is the preference holding a store's label, by the store's id: AutoDoc's own name
// for it, which survives the store's move (M) and never touches its files.
const todoLabelPref = "tui.todos.label."

// todoFollow is a task the cursor is to follow once its tab (and month) is read: a task moved to
// another status, or one just added.
type todoFollow struct {
	id, month string
	tab       int
}

// todoStore is one store the dropdown offers: where it is, as the todos.* verbs take it, and what
// the daemon says of it.
type todoStore struct {
	id       string // its key: "ws:<name>" or "root:<dir>"
	at       any    // a workspace name, or {root: …}
	sid      string // the daemon's id of the store, its label's key; "" when not known
	name     string // the derived name (storeName)
	alias    string // the user's label; "" shows the name
	ws       string // the workspace it is the store of, or ""
	path     string // the store directory, once known
	released bool   // todos.release gave it back: the daemon only reads it, so no write is offered
}

// title is how the store is called: its label, or its derived name.
func (s *todoStore) title() string { return cmp.Or(s.alias, s.name) }

// labelKey is the store's label's preference: by its id, or its path while the id is not known.
func (s *todoStore) labelKey() string { return todoLabelPref + cmp.Or(s.sid, s.path) }

// storeName is a store's derived name: its project's folder. A KB folder (kb, alone or in
// .auto-agents or .auto-agents-config) is named for the project that holds it, the first folder
// above it that is none of those: …/WebShop/.auto-agents/kb is WebShop.
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

// tildePath is path with the home directory (env's, nil for AutoDoc's own) as ~, as the panel shows
// a store's place.
func tildePath(env procenv.Env, path string) string {
	home, err := userdirs.Home(env)
	if err != nil || home == "" {
		return path
	}
	// compared in the slash form, the daemon's for a store's path, whichever form either is in
	p, h := paths.Slash(path), paths.Slash(home)
	if p == h || strings.HasPrefix(p, strings.TrimSuffix(h, "/")+"/") {
		return "~" + strings.TrimPrefix(p, h)
	}
	return path
}

// storeRoot is the project root of a store directory: the folder holding its .todo-list.
func storeRoot(path string) string {
	if dir, ok := strings.CutSuffix(path, "/.todo-list"); ok { // the daemon's slash form, its volume kept
		return dir
	}
	return path
}

// todos is the feature.
type todos struct {
	c feature.Core
	// stores are the dropdown's, in its order; cur is the one shown
	stores    []*todoStore
	storeRows *feature.Rows[*todoStore]
	cur       *todoStore
	// storeIndex is the dropdown's row as last set (-2 before the first)
	storeIndex int
	// want is the store to show once the stores are listed again (one just loaded); "" keeps cur
	want string
	// tab is the tab shown; month the archived month open in the Archived tab, "" for none
	tab   int
	month string
	// tasks are each tab's rows; months the Archived tab's; cursors each tab's row under the
	// cursor, -1 for none; stale the tabs whose rows a change has outdated, read again when shown
	tasks   [5]*feature.Rows[map[string]any]
	months  *feature.Rows[map[string]any]
	cursors [5]int
	stale   [5]bool
	// monthCursor is the months' row under their cursor
	monthCursor int
	// rootsSeq is the latest listing of the stores asked for, readSeq the latest read of a tab and
	// monthSeq of a month: an older answer is dropped; a write's answer never is
	rootsSeq, readSeq, monthSeq uint64
	// gen is the store's generation, one more each time another is shown: a read asked of an
	// older store is dropped, its rows not this one's
	gen      uint64
	folderAs string // what the folder chosen is for: "load", "new" or "move"
	// labelling is the store L is labelling, while its prompt is open
	labelling *todoStore
	// assignPick is A's picker of AutoDoc's agents; assigning the A in progress (todos_assign.go)
	assignPick *feature.Pick[feature.AgentEntry]
	assigning  *todoAssign
	// statusPick is s's picker of the statuses; statusing is the task it is for, while it is open
	statusPick *feature.Pick[todoStatusChoice]
	statusing  *todoStatusing
	follow     todoFollow
	detail     bool // the details show (i, o)
	// malformed are the files a tab's read could not list, by the tab's name: the one thing the
	// status line says (a store shown afresh empties it)
	malformed map[string]int
	// vars are the Variables tab's rows (todos_vars.go), varCursor its row under the cursor, varsSeq
	// its latest read, varEditing the variable its prompt is for
	vars       *feature.Rows[map[string]any]
	varCursor  int
	varsSeq    uint64
	varEditing *todoVarEdit
}

func newTodos(c feature.Core) *todos {
	t := &todos{c: c, detail: true, malformed: map[string]int{}, storeIndex: -2,
		storeRows: feature.NewRows(tuidecl.NewListModel("key", "label"), func(_ int, s *todoStore) feature.RowOf {
			return feature.RowOf{"key": s.id, "label": s.title()}
		}),
		months: feature.NewRows(tuidecl.NewListModel("key", "month", "count"), func(_ int, m map[string]any) feature.RowOf {
			return feature.RowOf{"key": feature.Str(m, "month"), "month": feature.Str(m, "month"), "count": fmt.Sprint(m["files"])}
		})}
	for i := range t.tasks {
		t.tasks[i] = feature.NewRows(tuidecl.NewListModel("key", "n", "title"), taskRowOf)
		t.cursors[i] = -1
	}
	t.vars, t.varCursor = newVarRows(c), -1
	t.assignPick = newAssignPick(t)
	t.statusPick = newStatusPick(t)
	return t
}

// taskRowOf is a task as its tab's row: numbered, its title marked ⚠ when its file has errors.
func taskRowOf(i int, x map[string]any) feature.RowOf {
	title := feature.Str(x, "title")
	if len(feature.AsList(x["errors"])) > 0 {
		title = "⚠ " + title
	}
	return feature.RowOf{"key": feature.Str(x, "id"), "n": strconv.Itoa(i + 1), "title": title}
}

func (*todos) Name() string { return "todos" }

func (t *todos) State() map[string]any {
	st := map[string]any{
		"App.todosTitle": "todos", "App.todosStatus": "", "App.todosStatusShown": false, "App.todoDetail": "",
		"App.todoDetailShown": true, "App.todoTab": 0,
		"App.todoStores": t.storeRows.Model(), "App.todoStoreIndex": -1, "App.todoHasStore": false,
		"App.todoMonths": t.months.Model(), "App.todoMonthIndex": -1,
		"App.todoFolder": "", "App.todoLabelText": "",
		"App.todoReadOnly": false, "App.todoActAdd": false,
		"App.todoActStatus": false, "App.todoActAssign": false, "App.todoActRemove": false,
		"App.todoStatusRows": t.statusPick.List.Model(), "App.todoStatusField": "", "App.todoStatusList": "statuses",
		"App.todoStatusPreviewTitle": "", "App.todoStatusPreviewText": "", "App.todoStatusPreviewAt": 0,
		"App.agentAssignRows": t.assignPick.List.Model(), "App.agentAssignField": "", "App.agentAssignStatus": "agents",
		"App.agentAssignPreviewTitle": "", "App.agentAssignPreviewText": "", "App.agentAssignPreviewAt": 0,
		"App.todoAssignTitle": "assign", "App.todoAssignText": "",
		"App.todoVarsRows": t.vars.Model(), "App.todoVarsIndex": -1, "App.todoOnVars": false,
		"App.todoActVarSet": false, "App.todoActVarAdd": false, "App.todoActVarClear": false,
		"App.todoVarTitle": "", "App.todoVarText": "",
	}
	for i, tab := range todoTabs {
		st["App.todo"+tab.stem+"Rows"] = t.tasks[i].Model()
		st["App.todo"+tab.stem+"Index"] = -1
	}
	return st
}

// twoNumbers takes two whole numbers: a tab and a row of its table.
func twoNumbers(what string, fn func(int, int)) feature.Command {
	return feature.Command{Args: []qml.SpecValueKind{feature.ArgNum, feature.ArgNum}, What: what, Run: func(a []qml.SpecValue) error {
		x, err1 := strconv.Atoi(a[0].Raw)
		y, err2 := strconv.Atoi(a[1].Raw)
		if err1 != nil || err2 != nil {
			return feature.ArgError(fmt.Sprintf("takes %s, not %s and %s", what, a[0].Raw, a[1].Raw))
		}
		fn(x, y)
		return nil
	}}
}

func (t *todos) Commands() map[string]feature.Command {
	return map[string]feature.Command{
		"App.openTodos":        feature.None(t.toggle),
		"App.todosTab":         feature.OneNumber("a tab", t.showTab),
		"App.todoTabShown":     feature.OneNumber("a tab", t.tabShown),
		"App.todoTabBefore":    feature.None(func() { t.stepTab(-1) }),
		"App.todoTabAfter":     feature.None(func() { t.stepTab(1) }),
		"App.todoStoreChosen":  feature.OneNumber("a store", t.storeChosen),
		"App.todoFocusStore":   feature.None(func() { t.c.Post(func() { t.focus("todoStore") }) }),
		"App.todoMoved":        twoNumbers("a tab and a row", t.moved),
		"App.todoActivated":    twoNumbers("a tab and a row", t.activated),
		"App.todoMonthChosen":  feature.OneNumber("a month", t.monthChosen),
		"App.todoMonthMoved":   feature.OneNumber("a month", t.monthMoved),
		"App.todoMonthsBack":   feature.None(t.monthsBack),
		"App.todoEnter":        feature.None(t.enter),
		"App.todoAdd":          feature.None(t.add),
		"App.todoAddTitle":     feature.OneString("a title", t.addTitle),
		"App.todoRemove":       feature.None(t.remove),
		"App.todoChooseStatus": feature.None(t.chooseStatus),
		"App.todoLabel":        feature.None(t.label),
		"App.todoLabelSet":     feature.OneString("a label", t.labelSet),
		"App.todoAssign":       feature.None(t.assign),
		"App.todoAssignSet":    feature.OneString("an instruction", t.assignSet),
		"App.todoRefresh":      feature.None(t.refresh),
		"App.todoToggleDetail": feature.None(t.toggleDetail),
		"App.todoLoad":         feature.None(t.chooseLoad),
		"App.todoNew":          feature.None(t.chooseNew),
		"App.todoMove":         feature.None(func() { t.chooseFolder("move") }),
		"App.todoFolderChosen": feature.OneString("a folder", t.folderChosen),
		"App.todoVarMoved":     feature.OneNumber("a variable", t.varMoved),
		"App.todoVarEdit":      feature.None(t.varEdit),
		"App.todoVarMachine":   feature.None(t.varMachine),
		"App.todoVarSet":       feature.OneString("a value", t.varSet),
	}
}

// Catalog is the todos' user commands (feature/catalog.go): each runs what its App.* twin runs.
func (t *todos) Catalog() []feature.Entry {
	return []feature.Entry{
		{ID: "todos.open", Title: "todos", Verb: "todos", Help: "the todos panel, open or closed (SPC x)", Twin: "App.openTodos()",
			Run: func(feature.Invocation) error { t.toggle(); return nil }},
		feature.Builtin("todos.new_store", "a new todo store", "App.todoNew()", t.chooseNew),
		feature.Builtin("todos.load_store", "load a todo store", "App.todoLoad()", t.chooseLoad),
		feature.Builtin("todos.label_store", "label the todo store", "App.todoLabel()", t.label),
	}
}

// todosPanelSpec is the todos panel: at the left edge by default, its geometry kept per edge like
// the side panels', and large until the user sizes it (todosDefault). It shares the left edge with
// the explorer as two drawers do: each opens over the page, the later on top.
func todosPanelSpec() feature.PanelSpec {
	s := feature.SidePanelSpec("todos", "todoOpenTable", 60, func(p feature.PrefsView) string { return p.Value(prefTodosEdge) })
	s.Icon = "✓"
	s.Default = todosDefault
	return s
}

// todosDefault is the todos panel's size and length before the user sets them: about 65% of the
// window wide and 80% tall, centred along its edge, so the tabs, the details and the action row
// read whole (an 80-column screen gives it 52 columns). A size the user leaves it at, or a place
// they float it to, is kept and wins (panelGeometry, panelfloat.go).
func todosDefault(edge string) (size, length int) {
	if edge == "top" || edge == "bottom" {
		return 80, 65
	}
	return 65, 80
}

// Panels: the panel, its stores listed afresh each time it opens, the keyboard in the tab shown.
func (t *todos) Panels() []feature.PanelSpec {
	s := todosPanelSpec()
	s.Focus = t.tabView
	s.Opened = func(feature.PanelHost) { t.list(); t.focusTab() }
	return []feature.PanelSpec{s}
}

// todoKeysCard is ? in the todos panel: its keys by purpose, on the floating card of keys SPC's is
// (feature/keys.go). A key on it runs, the card closing first; ? again, q or Esc closes it.
const todoKeysCard = "todoKeys"

// The headings of the todos' card of keys, by purpose.
const (
	todoGroupMove   = "MOVE"
	todoGroupTabs   = "TABS"
	todoGroupTask   = "TASK"
	todoGroupStatus = "STATUS"
	todoGroupStore  = "STORE"
	todoGroupVars   = "VARIABLES (6)"
)

// Keys are the panel's, live while the keyboard is in the store's dropdown, the tabs or the action
// row, not in the details pane (a read-only Editor that lets the edit keys it refuses bubble): Enter
// is a table's own (activated). 1–5 and [ ] change the tab. ? opens their card, by purpose.
func (*todos) Keys() []feature.Key {
	keys := []feature.Key{
		{Scope: "todos", Seq: "a", Show: "a", Run: "App.todoAdd()"},
		{Scope: "todos", Seq: "d", Show: "d", Run: "App.todoRemove()"},
		{Scope: "todos", Seq: "s", Show: "s", Run: "App.todoChooseStatus()"},
		{Scope: "todos", Seq: "i", Show: "i", Run: "App.todoToggleDetail()"},
		{Scope: "todos", Seq: "o", Show: "o", Run: "App.todoToggleDetail()"},
		{Scope: "todos", Seq: "Shift+A", Show: "A", Run: "App.todoAssign()"},
		{Scope: "todos", Seq: "[", Show: "[", Run: "App.todoTabBefore()"},
		{Scope: "todos", Seq: "]", Show: "]", Run: "App.todoTabAfter()"},
		{Scope: "todos", Seq: "Backspace", Show: "Backspace", Run: "App.todoMonthsBack()"},
		{Scope: "todos", Seq: "Shift+S", Show: "S", Run: "App.todoFocusStore()"},
		{Scope: "todos", Seq: "Shift+N", Show: "N", Run: "App.todoNew()"},
		{Scope: "todos", Seq: "Shift+O", Show: "O", Run: "App.todoLoad()"},
		{Scope: "todos", Seq: "Shift+L", Show: "L", Run: "App.todoLabel()"},
		{Scope: "todos", Seq: "Shift+R", Show: "R", Run: "App.todoRefresh()"},
		{Scope: "todos", Seq: "Shift+M", Show: "M", Run: "App.todoMove()"},
		{Scope: "todos", Seq: "?", Show: "?", Opens: todoKeysCard},
	}
	for i := range varsTab + 1 {
		keys = append(keys, feature.Key{Scope: "todos", Seq: fmt.Sprint(i + 1), Show: "1–6", Run: fmt.Sprintf("App.todosTab(%d)", i)})
	}
	keys = append(keys, feature.Key{Scope: "todos", Seq: "e", Show: "e", Run: "App.todoVarEdit()"},
		feature.Key{Scope: "todos", Seq: "m", Show: "m", Run: "App.todoVarMachine()"})
	card := slices.Concat(
		feature.InGroup(todoGroupMove, []feature.Key{
			{Seq: "Return", Show: "Enter", Does: "open the task's file, or a month", Run: "App.todoEnter()"},
			{Seq: "Backspace", Show: "Backspace", Does: "the months (Archived)", Run: "App.todoMonthsBack()"},
			{Seq: "i", Also: []string{"o"}, Show: "i o", Does: "the details, shown or hidden", Run: "App.todoToggleDetail()"},
		}),
		feature.InGroup(todoGroupTabs, []feature.Key{
			{Seq: "[", Show: "[ ]", Does: "the tab before, after", Run: "App.todoTabBefore()"},
			{Seq: "]", Run: "App.todoTabAfter()"},
			{Seq: "1", Show: "1–6", Does: "a tab", Run: "App.todosTab(0)"},
			{Seq: "2", Run: "App.todosTab(1)"}, {Seq: "3", Run: "App.todosTab(2)"},
			{Seq: "4", Run: "App.todosTab(3)"}, {Seq: "5", Run: "App.todosTab(4)"},
			{Seq: "6", Run: "App.todosTab(5)"},
		}),
		feature.InGroup(todoGroupStatus, []feature.Key{
			{Seq: "s", Show: "s", Does: "status…: move the task", Run: "App.todoChooseStatus()"},
		}),
		feature.InGroup(todoGroupTask, []feature.Key{
			{Seq: "a", Show: "a", Does: "add a task", Run: "App.todoAdd()"},
			{Seq: "d", Show: "d", Does: "remove the task", Run: "App.todoRemove()"},
			{Seq: "Shift+A", Show: "A", Does: "assign it to an agent", Run: "App.todoAssign()"},
		}),
		feature.InGroup(todoGroupVars, []feature.Key{
			{Seq: "e", Show: "e", Does: "set (file), a adds, d clears", Run: "App.todoVarEdit()"},
			{Seq: "m", Show: "m", Does: "set for this machine only", Run: "App.todoVarMachine()"},
		}),
		feature.InGroup(todoGroupStore, []feature.Key{
			{Seq: "Shift+S", Show: "S", Does: "choose the store", Run: "App.todoFocusStore()"},
			{Seq: "Shift+N", Show: "N", Does: "a new store", Run: "App.todoNew()"},
			{Seq: "Shift+O", Show: "O", Does: "load a store", Run: "App.todoLoad()"},
			{Seq: "Shift+L", Show: "L", Does: "rename it (its label)", Run: "App.todoLabel()"},
			{Seq: "Shift+R", Show: "R", Does: "refresh", Run: "App.todoRefresh()"},
			{Seq: "Shift+M", Show: "M", Does: "move the store", Run: "App.todoMove()"},
		}),
	)
	return append(keys, feature.OnCard(todoKeysCard, card)...)
}

// Picks: A's picker of AutoDoc's agents (todos_assign.go), and s's of the statuses.
func (t *todos) Picks() map[string]feature.Picker {
	return map[string]feature.Picker{"agentAssign": t.assignPick, "todoStatus": t.statusPick}
}

// Cards: ?'s card of the panel's keys.
func (*todos) Cards() []feature.Card {
	return []feature.Card{{ID: todoKeysCard, View: "TodoKeys", Title: "todos — keys", Toggle: "?",
		Note:   "↑ ↓ or j k move · q closes the panel",
		Help:   "THE TODOS PANEL'S KEYS (? in the todos panel)",
		Groups: [][]string{{todoGroupMove, todoGroupTabs, todoGroupStatus}, {todoGroupTask, todoGroupVars, todoGroupStore}}}}
}

func (t *todos) chooseLoad() { t.chooseFolder("load") }
func (t *todos) chooseNew()  { t.chooseFolder("new") }

// todoFolderDialogs are the folder dialog for each of Load, New and M, by what the folder is for:
// each says it in its title, set when it is built (a FolderDialog's title cannot be bound).
var todoFolderDialogs = map[string]string{"load": "todoFolder", "new": "todoFolderNew", "move": "todoFolderMove"}

// Kinds: another client's change to the store shown reads its tabs again.
func (t *todos) Kinds() []string { return []string{"todos.changed"} }

func (t *todos) Event(_, workspace, detail string) {
	s := t.cur
	if s == nil {
		return
	}
	ws, isWorkspace := s.at.(string)
	root := rootOf(s.at)
	if isWorkspace && ws == workspace || root != "" && (detail == root || strings.HasPrefix(detail, root+"#")) {
		t.reload()
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
		t.c.Notify(t.c.Tr("autodoc.notice.todos.todos_enter_a_workspace_first_or_load_a_store"))
		return
	}
	t.c.TogglePanel(todosPanelSpec().ID)
}

// call runs a todos verb off the loop and hands its answer to then on the loop. With a sequence,
// only the latest call of that sequence is answered, its error as well as its result; without
// one (a write), every call is.
func (t *todos) call(verb string, params []any, seq *uint64, then func(any)) {
	t.callOr(verb, params, seq, then, nil)
}

// callOr is call whose failure, once told, runs failed (nil for nothing more).
func (t *todos) callOr(verb string, params []any, seq *uint64, then func(any), failed func()) {
	tk := feature.Ticket{}
	if seq != nil {
		tk = t.c.Ticket(seq, feature.TicketSession)
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
			if failed != nil {
				failed()
			}
			return
		}
		then(v.(answer).v)
	})
}

// answer wraps a verb's result, so a nil one is not taken for an error.
type answer struct{ v any }

// list reads the stores: the workspace's first, as todos.resolve finds it, then every other the
// daemon knows by a root of its own, each called by its label or its derived name.
func (t *todos) list() {
	t.call("todos.stores", nil, &t.rootsSeq, func(v any) {
		var stores []*todoStore
		ids := map[string]string{} // store path → the daemon's id
		for _, x := range feature.AsList(v) {
			m := feature.AsMap(x)
			ids[feature.Str(m, "path")] = feature.Str(m, "id")
			roots := feature.AsList(m["roots"])
			if len(roots) == 0 {
				continue // a store with no project root is not one the verbs can reach
			}
			root, _ := roots[0].(string)
			stores = append(stores, &todoStore{id: "root:" + root, at: map[string]any{"root": root}, sid: feature.Str(m, "id"),
				name: storeName(root), path: feature.Str(m, "path"), released: m["released"] == true})
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
				r := feature.AsMap(v)
				path := feature.Str(r, "store")
				w := &todoStore{id: "ws:" + ws, at: ws, ws: ws, sid: ids[path], name: storeName(storeRoot(path)), path: path,
					released: r["released"] == true}
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
	tk := t.c.Ticket(&t.rootsSeq, feature.TicketSession)
	t.c.Go(func(ctx context.Context) any {
		v, err := t.c.Call(ctx, "preference.list")
		if err != nil {
			return map[string]any{}
		}
		return feature.AsMap(v)
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

// show puts stores in the dropdown and shows one: the one asked for (want), else the one shown
// before when it is still listed, else the first. Another store than before is read afresh; the
// same one, its tab read again.
func (t *todos) show(stores []*todoStore) {
	t.stores = stores
	t.storeRows.Reset(stores)
	want := cmp.Or(t.want, func() string {
		if t.cur != nil {
			return t.cur.id
		}
		return ""
	}())
	t.want = ""
	i := slices.IndexFunc(stores, func(s *todoStore) bool { return s.id == want })
	if i < 0 && len(stores) > 0 {
		i = 0
	}
	t.selectStore(i)
	t.c.Set("App.todoHasStore", i >= 0)
	if i < 0 {
		t.cur = nil
		t.showStore()
		return
	}
	if t.cur != nil && t.cur.id == stores[i].id {
		t.cur = stores[i] // its label or its claim may have changed
		t.showDetail()
		t.reload()
		return
	}
	t.cur = stores[i]
	t.showStore()
}

// shows is whether s is the store shown, by its id: listing the stores again (the panel opened, a
// label set, a store registered) makes the same store a new value while a write is out, and its
// answer is still the shown store's.
func (t *todos) shows(s *todoStore) bool { return s != nil && t.cur != nil && t.cur.id == s.id }

// selectStore puts the dropdown on row i, -1 for none: only when it is not there already, as a
// dropdown set again under the user could take the keyboard from where it is.
func (t *todos) selectStore(i int) {
	if i != t.storeIndex {
		t.storeIndex = i
		t.c.Set("App.todoStoreIndex", i)
	}
}

// storeChosen is a store chosen in the dropdown: its tabs, read afresh.
func (t *todos) storeChosen(i int) {
	if i < 0 || i >= len(t.stores) {
		return
	}
	t.selectStore(i) // the dropdown shows it already: its source follows
	if t.cur == t.stores[i] {
		return
	}
	t.cur = t.stores[i]
	t.showStore()
}

// showStore empties the tabs for the store now shown and reads the tab shown: a read asked of the
// store before is dropped, its rows not this one's.
func (t *todos) showStore() {
	t.gen++
	t.month, t.follow = "", todoFollow{}
	t.months.Reset(nil)
	for i := range t.tasks {
		t.tasks[i].Reset(nil)
		t.cursors[i] = -1
		t.stale[i] = true
	}
	t.vars.Reset(nil)
	t.varCursor = -1
	t.malformed = map[string]int{}
	t.status()
	t.read()
	t.showDetail()
}

// reload marks every tab outdated and reads the one shown again: after a write, an event, R.
func (t *todos) reload() {
	for i := range t.stale {
		t.stale[i] = true
	}
	t.read()
}

// read reads the tab shown of the store shown: a status's tasks; for Archived its months, and the
// open month's tasks once they are listed.
func (t *todos) read() {
	s, tab, gen := t.cur, t.tab, t.gen
	if s == nil {
		return
	}
	if tab == varsTab {
		t.readVars()
		return
	}
	if tab != archivedTab {
		t.call("todos.list", []any{s.at, map[string]any{"buckets": []any{todoBuckets[tab].bucket}}}, &t.readSeq, func(v any) {
			if gen == t.gen {
				t.applyTasks(tab, todoBuckets[tab].title, v)
			}
		})
		return
	}
	t.call("todos.list", []any{s.at, map[string]any{"buckets": []any{"archived"}}}, &t.readSeq, func(v any) {
		if gen != t.gen {
			return
		}
		var months []map[string]any
		for _, p := range feature.AsList(feature.AsMap(v)["partitions"]) {
			months = append(months, feature.AsMap(p))
		}
		t.months.Reset(months)
		t.stale[archivedTab] = false
		if !slices.ContainsFunc(months, func(m map[string]any) bool { return feature.Str(m, "month") == t.month }) {
			t.month = "" // the month is gone
		}
		t.selectMonth()
		if t.month == "" {
			t.tasks[archivedTab].Reset(nil)
			t.cursors[archivedTab] = -1
			t.showDetail()
			return
		}
		t.readMonth()
	})
}

// readMonth reads the open archived month's tasks.
func (t *todos) readMonth() {
	s, month, gen := t.cur, t.month, t.gen
	t.call("todos.list", []any{s.at, map[string]any{"buckets": []any{"archived/" + month}}}, &t.monthSeq, func(v any) {
		if gen == t.gen && month == t.month {
			t.applyTasks(archivedTab, "Archived "+month, v)
		}
	})
}

// applyTasks puts a read's tasks in tab's table, errored ones first, and keeps the cursor on the
// task it was on, or on the task the cursor follows.
func (t *todos) applyTasks(tab int, name string, v any) {
	l := feature.AsMap(v)
	var errored, clean []map[string]any
	for _, x := range feature.AsList(l["tasks"]) {
		m := feature.AsMap(x)
		if len(feature.AsList(m["errors"])) > 0 {
			errored = append(errored, m)
		} else {
			clean = append(clean, m)
		}
	}
	tasks := append(errored, clean...)
	keep := feature.Str(t.taskAt(tab, t.cursors[tab]), "id")
	if f := t.follow; f.id != "" && f.tab == tab && (tab != archivedTab || f.month == t.month) {
		keep, t.follow = f.id, todoFollow{}
	}
	t.tasks[tab].Reset(tasks)
	if tab != archivedTab || t.month != "" {
		t.stale[tab] = false
	}
	t.malformed[name] = len(feature.AsList(l["malformed"]))
	t.status()
	at := slices.IndexFunc(tasks, func(x map[string]any) bool { return feature.Str(x, "id") == keep })
	if at < 0 && len(tasks) > 0 {
		at = min(max(t.cursors[tab], 0), len(tasks)-1)
	}
	t.selectRow(tab, at)
}

// selectRow puts tab's cursor on row i, -1 for none: its table's, and the details.
func (t *todos) selectRow(tab, i int) {
	t.cursors[tab] = i
	src := "App.todo" + todoTabs[tab].stem + "Index"
	t.c.Set(src, -1) // moved away first, so the same row twice still moves the table's cursor
	t.c.Set(src, i)
	if tab == t.tab {
		t.showDetail()
	}
}

// selectMonth puts the months' cursor on the month open.
func (t *todos) selectMonth() {
	i := slices.IndexFunc(t.months.All(), func(m map[string]any) bool { return feature.Str(m, "month") == t.month })
	t.c.Set("App.todoMonthIndex", -1)
	t.c.Set("App.todoMonthIndex", i)
}

// LanguageChanged composes the panel's variable rows, status line and details again, in the UI's
// language.
func (t *todos) LanguageChanged() {
	t.vars.Reset(t.vars.All()) // the variables' rows: "(unset)", "(set per machine: none here)"
	t.pointVarsAt(t.varCursor) // where the cursor was: a panel stays open through a switch
	t.status()
	t.showDetail()
}

// status is the panel's status line: the one thing it says is a file a tab could not list, which
// needs fixing by hand; nothing else, so it is hidden while none is.
func (t *todos) status() {
	var where []string
	n := 0
	for name, bad := range t.malformed {
		if bad > 0 {
			where = append(where, t.c.Trf("autodoc.text.todos.in", bad, t.listName(name)))
			n += bad
		}
	}
	slices.Sort(where)
	msg := ""
	if n > 0 {
		msg = t.c.Trf("autodoc.text.todos.malformed_files_not_listed", n, strings.Join(where, ", "))
	}
	t.c.Set("App.todosStatus", msg)
	t.c.Set("App.todosStatusShown", msg != "")
}

// listName is a read list's name (a tab's title, or "Archived <month>", malformed's keys) in the UI's
// language.
func (t *todos) listName(name string) string {
	if month, ok := strings.CutPrefix(name, "Archived "); ok {
		return t.c.Trf("autodoc.text.todos.archived_month", month)
	}
	switch name {
	case "Open":
		return t.c.Tr("autodoc.text.todos.open")
	case "In progress":
		return t.c.Tr("autodoc.text.todos.in_progress")
	case "Deferred":
		return t.c.Tr("autodoc.text.todos.deferred")
	case "Completed":
		return t.c.Tr("autodoc.text.todos.completed")
	case "Archived":
		return t.c.Tr("autodoc.text.todos.archived")
	}
	return name
}

// showTab is a tab shown (a click on its title, 1–6, [ ]): read when its rows are outdated (the
// Variables tab: every time), the keyboard in its table.
func (t *todos) showTab(i int) {
	if i < 0 || i > varsTab {
		return
	}
	if i != t.tab {
		t.tab = i
		t.c.Set("App.todoTab", i)
		t.showDetail()
	}
	if i == varsTab || t.stale[i] {
		t.read()
	}
	t.focusTab()
}

// tabShown is the tab bar's own change (a click on a title, Ctrl+PageUp/Down), and its echo of a
// change made here: only a change shows the tab, so an echo arriving late never takes the keyboard
// from where it went since.
func (t *todos) tabShown(i int) {
	if i != t.tab {
		t.showTab(i)
	}
}

// stepTab is [ and ]: the tab before or after, round.
func (t *todos) stepTab(step int) {
	n := varsTab + 1
	t.showTab(((t.tab+step)%n + n) % n)
}

// tabView is what in the tab shown takes the keyboard: its table, or the Archived tab's months while
// no month is open. The panel's toggle reaches for it: a table on a tab not shown takes no focus, so
// the keyboard would never enter the panel, and a covered panel comes forward as the keyboard enters.
func (t *todos) tabView() string {
	switch {
	case t.tab == varsTab:
		return "todoVarsTable"
	case t.tab == archivedTab && t.month == "":
		return "todoMonths"
	}
	return todoTabs[t.tab].table
}

// focusTab gives the tab shown's table the keyboard, once the tab is laid out.
func (t *todos) focusTab() {
	id := t.tabView()
	t.c.Post(func() { t.focus(id) })
}

// focus gives view id the keyboard.
func (t *todos) focus(id string) {
	if err := t.c.View(id, "forceActiveFocus"); err != nil {
		t.c.Failed("todos: the keyboard", err)
	}
}

// taskAt is row i of tab's table; nil for none.
func (t *todos) taskAt(tab, i int) map[string]any {
	x, _ := t.tasks[tab].At(i)
	return x
}

// cursor is the store shown, and the task under the cursor in the tab shown, nil for none (and on
// the Variables tab).
func (t *todos) cursor() (*todoStore, map[string]any) {
	if t.tab == varsTab {
		return t.cur, nil
	}
	return t.cur, t.taskAt(t.tab, t.cursors[t.tab])
}

// moved is a table's cursor moving: tab's row under it kept, the details its task's. A table not
// shown (its rows read again) moves only its own cursor. A table with rows is never on no row: its
// -1 is selectRow moving it away for a moment, and the actions must not hide under a click then.
func (t *todos) moved(tab, i int) {
	if tab < 0 || tab >= len(t.cursors) || i < 0 && t.tasks[tab].Len() > 0 {
		return
	}
	t.cursors[tab] = i
	if tab == t.tab {
		t.showDetail()
	}
}

// showDetail shows the details of the task under the cursor, the store's place as the heading, and
// the actions that apply. It runs when the cursor moves and when an answer lands, never inside a
// button's own click, so no button hides itself (a golib Button's handler must not).
func (t *todos) showDetail() {
	s, x := t.cursor()
	heading := "todos"
	if s != nil && s.path != "" {
		heading += " · " + tildePath(t.c.Env(), s.path)
	}
	t.c.Set("App.todosTitle", heading)
	if v := t.varAt(t.varCursor); t.tab == varsTab && v != nil {
		t.c.Set("App.todoDetail", varDetail(t.c, v))
	} else if x != nil {
		t.c.Set("App.todoDetail", todoDetail(x))
	} else {
		t.c.Set("App.todoDetail", "")
	}
	// Posted: the cursor's move reaches here from a table's currentIndexChanged, a Bus delivery,
	// and a button hidden there could not end a gesture it held (golib's Bus subscribers enqueue
	// an Update for that). Read when it runs, so the cursor is where it is then.
	t.c.Post(t.syncActions)
}

// syncActions sets the action row: the actions that apply to the task under the cursor, the others
// hidden, never shown greyed. On a store taking writes here, Status…, Assign and Remove on a task,
// and Add always. A released store shows no action at all, only that it is read-only, and its
// writes' keys do nothing.
func (t *todos) syncActions() {
	s, x := t.cursor()
	writable := !t.readOnly(s)
	task := writable && x != nil
	onVars := t.tab == varsTab
	t.c.Set("App.todoReadOnly", s != nil && s.released)
	t.c.Set("App.todoOnVars", onVars)
	t.c.Set("App.todoActAdd", writable && !onVars)
	t.c.Set("App.todoActStatus", task)
	t.c.Set("App.todoActAssign", task)
	t.c.Set("App.todoActRemove", task)
	if onVars {
		t.syncVarActions()
	} else {
		for _, src := range []string{"App.todoActVarSet", "App.todoActVarAdd", "App.todoActVarClear"} {
			t.c.Set(src, false)
		}
	}
}

// todoDetail is a task's details, as autofinder's i shows them.
func todoDetail(x map[string]any) string {
	var b strings.Builder
	b.WriteString("## " + feature.Str(x, "title") + "\n\n")
	for _, f := range []string{"status", "priority", "assignee", "due", "created", "updated"} {
		if v := feature.Str(x, f); v != "" {
			fmt.Fprintf(&b, "- %s: %s\n", f, v)
		}
	}
	for _, f := range []string{"tags", "adr", "review", "blocked"} {
		for _, v := range feature.AsList(x[f]) {
			fmt.Fprintf(&b, "- %s: %v\n", f, v)
		}
	}
	if errs := feature.AsList(x["errors"]); len(errs) > 0 {
		b.WriteString("\nerrors:\n\n")
		for _, e := range errs {
			m := feature.AsMap(e)
			fmt.Fprintf(&b, "- %s %s: %s\n", feature.Str(m, "field"), feature.Str(m, "code"), feature.Str(m, "message"))
		}
	}
	if d := strings.TrimSpace(feature.Str(x, "description")); d != "" {
		b.WriteString("\n" + d + "\n")
	}
	return b.String()
}

// activated is Enter on tab's row i: the task's file opens in the editor, the panel closing.
func (t *todos) activated(tab, i int) {
	s, x := t.cur, t.taskAt(tab, i)
	if s == nil || x == nil || s.path == "" {
		return
	}
	if err := t.c.View(todosPanelSpec().ID, "close"); err != nil {
		t.c.Failed("todos: open", err)
	}
	t.c.OpenFile(filepath.Join(s.path, filepath.FromSlash(feature.Str(x, "path"))))
}

// enter is Enter on the card of keys: the task under the cursor opens, as Enter in its table; on
// the Archived tab with no month open, the month under the months' cursor opens.
func (t *todos) enter() {
	if t.tab == varsTab {
		t.varEdit()
		return
	}
	if t.tab == archivedTab && t.month == "" {
		t.monthChosen(t.monthCursor)
		return
	}
	t.activated(t.tab, t.cursors[t.tab])
}

// monthChosen is Enter on an archived month: its tasks read, the keyboard in them.
func (t *todos) monthChosen(i int) {
	m, ok := t.months.At(i)
	if !ok {
		return
	}
	t.month = feature.Str(m, "month")
	t.tasks[archivedTab].Reset(nil)
	t.cursors[archivedTab] = -1
	t.readMonth()
	t.c.Post(func() { t.focus(todoTabs[archivedTab].table) })
}

// monthMoved is the months' cursor moving.
func (t *todos) monthMoved(i int) { t.monthCursor = i }

// monthsBack is Backspace: on the Archived tab, the keyboard back in its months.
func (t *todos) monthsBack() {
	if t.tab == archivedTab {
		t.c.Post(func() { t.focus("todoMonths") })
	}
}

// readOnly reports that store s takes no writes here: todos.release gave it back. It says nothing:
// the action row says the store is read-only, and offers no write (syncActions), so a write's key
// there does nothing rather than answer with an error.
func (t *todos) readOnly(s *todoStore) bool { return s == nil || s.released }

// write runs a todos verb that changes store s, once s takes writes here, and reads its tabs again:
// after a refused write too, whose row may be of the task as it was (its rev stale).
func (t *todos) write(s *todoStore, verb string, params []any, done func(any)) {
	if t.readOnly(s) {
		return
	}
	reload := func() {
		if t.shows(s) {
			t.reload()
		}
	}
	t.callOr(verb, params, nil, func(v any) {
		if done != nil {
			done(v)
		}
		reload()
	}, reload)
}

// add is a: the title of a new task in the store shown asked for (todoNew).
func (t *todos) add() {
	if t.tab == varsTab {
		t.varAdd()
		return
	}
	if !t.readOnly(t.cur) {
		t.c.Open("todoNew")
	}
}

// addTitle adds the task titled: it is open, so the Open tab shows, the cursor on it.
func (t *todos) addTitle(title string) {
	t.c.Close("todoNew")
	s := t.cur
	if s == nil || strings.TrimSpace(title) == "" {
		return
	}
	t.write(s, "todos.add", []any{s.at, map[string]any{"title": strings.TrimSpace(title)}}, func(v any) {
		task := feature.AsMap(v)
		t.c.Notify(t.c.Trf("autodoc.notice.todos.todos_added", feature.Str(task, "id")))
		t.followTask(task, cmp.Or(feature.Str(task, "status"), "open"))
	})
}

func (t *todos) remove() {
	if t.tab == varsTab {
		t.varClear()
		return
	}
	s, x := t.cursor()
	if x == nil || t.readOnly(s) {
		return
	}
	t.c.Ask(feature.Question{Title: t.c.Tr("autodoc.question.todos.remove_the_task"), Text: t.c.Trf("autodoc.question.todos.remove_its_file", strconv.Quote(feature.Str(x, "title"))),
		OnYes: func() { t.removeConfirmed(s, x) }})
}

// removeConfirmed removes the task asked about, whatever row the cursor is on by then.
func (t *todos) removeConfirmed(s *todoStore, x map[string]any) {
	t.write(s, "todos.remove", []any{s.at, feature.Str(x, "id"), feature.Str(x, "rev")}, func(any) { t.c.Notify(t.c.Trf("autodoc.notice.todos.todos_removed", feature.Str(x, "id"))) })
}

// moveTo gives task x of store s its next status, through todos.status so its events fire; the
// tab of that status shows once the daemon has moved it, the cursor on the task.
func (t *todos) moveTo(s *todoStore, x map[string]any, next string) {
	t.write(s, "todos.status", []any{s.at, feature.Str(x, "id"), feature.Str(x, "rev"), next}, func(v any) {
		task := feature.AsMap(v)
		if feature.Str(task, "id") == "" {
			task = map[string]any{"id": feature.Str(x, "id")}
		}
		t.followTask(task, next)
	})
}

// followTask shows the tab of status, the cursor to follow task there once the write's read
// lands: into Archived, the month its file went to.
func (t *todos) followTask(task map[string]any, status string) {
	tab := tabOf(status)
	if tab < 0 || feature.Str(task, "id") == "" {
		return
	}
	f := todoFollow{id: feature.Str(task, "id"), tab: tab}
	if tab == archivedTab {
		f.month = monthOf(feature.Str(task, "path"))
		t.month = f.month
	}
	t.follow = f
	if tab != t.tab {
		t.tab = tab
		t.c.Set("App.todoTab", tab)
	}
	t.focusTab()
}

// label is L, and the store bar's Edit: the store shown's label asked for (todoLabel), its own filled
// in. A label is AutoDoc's alone, so a read-only store takes one too.
func (t *todos) label() {
	s := t.cur
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
			t.c.Notify(t.c.Trf("autodoc.notice.todos.todos_shows_as_again", tildePath(t.c.Env(), s.path), s.name))
		} else {
			t.c.Notify(t.c.Trf("autodoc.notice.todos.todos_is_labelled", tildePath(t.c.Env(), s.path), text))
		}
		t.list()
	})
}

// refresh is R, and the store bar's Refresh: the daemon reconciles the store's active buckets, then the
// tab shown is read again.
func (t *todos) refresh() {
	s := t.cur
	if s == nil {
		return
	}
	if t.readOnly(s) {
		t.reload() // read-only: read again, nothing to reconcile
		return
	}
	t.write(s, "todos.refresh", []any{s.at}, func(v any) {
		r := feature.AsMap(v)
		t.c.Notify(t.c.Trf("autodoc.notice.todos.todos_refreshed_moved_archived_rewritten", r["moved"], r["archived"], r["rewritten"]))
	})
}

func (t *todos) toggleDetail() { t.detail = !t.detail; t.c.Set("App.todoDetailShown", t.detail) }

// chooseFolder opens the folder dialog for Load, New or M (move).
func (t *todos) chooseFolder(as string) {
	s := t.cur
	if as == "move" && t.readOnly(s) {
		return
	}
	t.folderAs = as
	start := ""
	if s != nil {
		start = rootOf(s.at)
		if start == "" && s.path != "" {
			start = filepath.Dir(s.path)
		}
	}
	t.c.Set("App.todoFolder", start)
	t.c.Open(todoFolderDialogs[as])
}

// folderChosen acts on the folder: Load registers the project and shows its store; New registers it
// and creates its store (a write, which claims it); M moves the store shown there.
func (t *todos) folderChosen(dir string) {
	t.c.Close(todoFolderDialogs[t.folderAs])
	if dir == "" {
		return
	}
	dir = filepath.Clean(dir)
	if filepath.Base(dir) == ".todo-list" {
		dir = filepath.Dir(dir) // the store itself was chosen: its project is the root
	}
	// the daemon's form, the slash form with its volume kept: a store's id from todos.stores is
	// "root:" and its root in that form, so the folder just chosen is found among them
	root := paths.Slash(dir)
	at := map[string]any{"root": root}
	switch t.folderAs {
	case "load":
		t.call("todos.register", []any{root}, nil, func(any) { t.want = "root:" + root; t.list() })
	case "new":
		t.call("todos.register", []any{root}, nil, func(any) {
			t.call("todos.resolve", []any{at}, nil, func(v any) {
				t.want = "root:" + root
				s := &todoStore{id: "root:" + root, at: at, name: storeName(dir), released: feature.AsMap(v)["released"] == true}
				if t.readOnly(s) { // the folder is loaded; a store released there takes no new buckets
					t.c.Notify(t.c.Trf("autodoc.notice.todos.todos_is_loaded_read_only_its_store_was_released", tildePath(t.c.Env(), dir)))
					t.list()
					return
				}
				t.write(s, "todos.scaffold", []any{at}, func(any) { t.c.Notify(t.c.Trf("autodoc.notice.todos.todos_a_new_store_in", dir)); t.list() })
			})
		})
	case "move":
		s := t.cur
		if s == nil {
			return
		}
		t.write(s, "todos.move_store", []any{s.at, paths.Join(root, ".todo-list")}, func(any) {
			t.c.Notify(t.c.Trf("autodoc.notice.todos.todos_the_store_is_now_in", dir))
			t.list()
		})
	}
}
