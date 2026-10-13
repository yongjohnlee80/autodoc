package tui

import (
	"context"
	"fmt"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// STATUS… — s in the todos panel: a picker of every status a task can have (open, in progress,
// deferred, completed, archived), the task's own marked, and choosing one moves the task there
// through todos.status, as any status change goes (todos.go: moveTo), the cursor following it. The
// daemon's rules are its own: a status is what it was asked for, and assigning is A's (D1).

// todoStatusChoice is a row of the picker: a status, and whether it is the task's now.
type todoStatusChoice struct {
	status  string
	current bool
}

// todoStatusing is the task s's picker is open for, read when s was pressed.
type todoStatusing struct {
	store *todoStore
	task  map[string]any
}

// newStatusPick is the picker of the statuses that s opens.
func newStatusPick(t *todos) *pick[todoStatusChoice] {
	return &pick[todoStatusChoice]{c: t.c, dialog: "todoStatusPicker", name: "todoStatus",
		list:   newRows(tuidecl.NewListModel("key", "status"), statusRowOf),
		field:  "App.todoStatusField",
		status: "App.todoStatusList", noun: "statuses",
		match: func(c todoStatusChoice) string { return c.status },
		preview: func(c todoStatusChoice) func(context.Context) answerOf[pane] {
			pn := t.statusPane(c)
			return func(context.Context) answerOf[pane] { return answerOf[pane]{v: pn} }
		},
		choose: t.statusChosen,
		cancel: func() { t.statusing = nil }}
}

// statusRowOf is a status as the picker lists it: the task's own marked ●.
func statusRowOf(_ int, c todoStatusChoice) rowOf {
	mark := "  "
	if c.current {
		mark = "● "
	}
	return rowOf{"key": c.status, "status": mark + c.status}
}

// statusPane is what choosing c does to the task the picker is for.
func (t *todos) statusPane(c todoStatusChoice) pane {
	if t.statusing == nil {
		return pane{}
	}
	title := str(t.statusing.task, "title")
	if c.current {
		return pane{title: c.status, text: fmt.Sprintf("## %s\n\n%s is %s now: choosing it changes nothing.\n", c.status, title, c.status)}
	}
	return pane{title: c.status, text: fmt.Sprintf("## %s\n\n%s moves from %s to %s.\n", c.status, title,
		str(t.statusing.task, "status"), c.status)}
}

// chooseStatus is s: the task under the cursor, in a store taking writes here, offered every
// status. Off a task, or on a store not claimed, it does nothing: the action row offers it only
// where it applies.
func (t *todos) chooseStatus() {
	s, x := t.cursor()
	if x == nil || t.readOnly(s) {
		return
	}
	t.statusing = &todoStatusing{store: s, task: x}
	now := str(x, "status")
	choices := make([]todoStatusChoice, len(todoStatuses))
	for i, st := range todoStatuses {
		choices[i] = todoStatusChoice{status: st, current: st == now}
	}
	t.statusPick.setAll(choices)
	t.statusPick.open()
}

// statusChosen is a status chosen: the task moves there, unless it is there already.
func (t *todos) statusChosen(c todoStatusChoice) {
	st := t.statusing
	t.statusing = nil
	if st == nil || c.current {
		return
	}
	t.moveTo(st.store, st.task, c.status)
}
