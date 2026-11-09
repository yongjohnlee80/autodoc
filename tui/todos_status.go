package tui

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/autodoc/tui/feature"
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
func newStatusPick(t *todos) *feature.Pick[todoStatusChoice] {
	return &feature.Pick[todoStatusChoice]{Core: t.c, Dialog: "todoStatusPicker", Name: "todoStatus",
		List:   feature.NewRows(tuidecl.NewListModel("key", "status"), statusRowOf),
		Field:  "App.todoStatusField",
		Status: "App.todoStatusList", Noun: "statuses",
		Match: func(c todoStatusChoice) string { return c.status },
		Preview: func(c todoStatusChoice) func(context.Context) feature.AnswerOf[feature.Pane] {
			pn := t.statusPane(c)
			return func(context.Context) feature.AnswerOf[feature.Pane] { return feature.AnswerOf[feature.Pane]{Value: pn} }
		},
		Choose: t.statusChosen,
		Cancel: func() { t.statusing = nil }}
}

// statusRowOf is a status as the picker lists it: the task's own marked ●.
func statusRowOf(_ int, c todoStatusChoice) feature.RowOf {
	mark := "  "
	if c.current {
		mark = "● "
	}
	return feature.RowOf{"key": c.status, "status": mark + c.status}
}

// statusPane is what choosing c does to the task the picker is for, under the task's title: short
// lines, as the preview does not wrap.
func (t *todos) statusPane(c todoStatusChoice) feature.Pane {
	if t.statusing == nil {
		return feature.Pane{}
	}
	title := feature.Str(t.statusing.task, "title")
	if c.current {
		return feature.Pane{Title: title, Text: fmt.Sprintf("## %s\n\n%s\n", c.status, t.c.Tr("autodoc.text.todos_status.its_status_now_choosing_it_changes_nothing"))}
	}
	return feature.Pane{Title: title, Text: fmt.Sprintf("## %s\n\n%s\n", c.status, t.c.Trf("autodoc.text.todos_status.from_to", feature.Str(t.statusing.task, "status"), c.status))}
}

// chooseStatus is s: the task under the cursor, in a store taking writes here, offered every
// status. Off a task, or on a released store, it does nothing: the action row offers it only
// where it applies.
func (t *todos) chooseStatus() {
	s, x := t.cursor()
	if x == nil || t.readOnly(s) {
		return
	}
	t.statusing = &todoStatusing{store: s, task: x}
	now := feature.Str(x, "status")
	choices := make([]todoStatusChoice, len(todoStatuses))
	for i, st := range todoStatuses {
		choices[i] = todoStatusChoice{status: st, current: st == now}
	}
	t.statusPick.SetAll(choices)
	t.statusPick.Open()
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
