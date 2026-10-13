package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// ASSIGN — A in the todos panel hands the task under the cursor to one of AutoDoc's own agents, as
// AutoVim's A hands one to its auto-agents: a picker of AutoDoc's agents by codename, an optional
// Instructions: line (Esc cancels; empty is "Task assigned to you."), then todos.assign with the
// assignee autodoc:<codename>, and delivery through the agents feature's one HandTask. The store's
// assignment is the record and delivery a best-effort side effect: a task not delivered (a stopped
// agent left stopped, an agent gone) stays assigned, and the notice says how to deliver it again.
// AutoDoc's agents only: it holds no AutoVim agent list and makes no AutoVim call.

// assignDefault is the instruction an empty Instructions: line gives, as AutoVim's A does.
const assignDefault = "Task assigned to you."

// assigneePrefix is an AutoDoc agent's assignee: autodoc:<codename>, never AutoVim's agent:<name>.
const assigneePrefix = "autodoc:"

// todoAssign is one A in progress: the task, the agent once the picker has chosen it, and the
// workspace it began in: HandTask delivers to the workspace in use, so an assign answered after a
// switch is handed to no one there.
type todoAssign struct {
	store *todoStore
	task  map[string]any
	agent AgentEntry
	ws    Ticket
}

// newAssignPick is the picker of AutoDoc's agents that A opens.
func newAssignPick(t *todos) *pick[AgentEntry] {
	return &pick[AgentEntry]{c: t.c, dialog: "agentAssignPicker", name: "agentAssign",
		list:   newRows(tuidecl.NewListModel("key", "agent", "state"), agentRowOf),
		field:  "App.agentAssignField",
		status: "App.agentAssignStatus", noun: "agents",
		match: func(e AgentEntry) string { return e.Name + " " + e.Ref.Codename },
		preview: func(e AgentEntry) func(context.Context) answerOf[pane] {
			pn := agentPane(e)
			return func(context.Context) answerOf[pane] { return answerOf[pane]{v: pn} }
		},
		scope:  TicketWorkspace,
		choose: t.assignTo}
}

func agentRowOf(_ int, e AgentEntry) rowOf {
	return rowOf{"key": e.Ref.Codename, "agent": e.Name + "  (" + e.Ref.Codename + ")", "state": runState(e)}
}

func runState(e AgentEntry) string {
	if e.Running {
		return "running"
	}
	return "stopped"
}

// agentPane is an agent's preview: who it is, what it runs, and what A does to a stopped one.
func agentPane(e AgentEntry) pane {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n- codename: %s\n- assignee: %s%s\n- command: `%s`\n- %s\n", e.Name, e.Ref.Codename,
		assigneePrefix, e.Ref.Codename, e.Command, runState(e))
	if !e.Running {
		b.WriteString("\nNot running: you'll be asked to start it, and the task is handed over once it has.\n")
	}
	if e.Orphan {
		b.WriteString("\nIts profile is gone; it takes the task while it runs.\n")
	}
	return pane{title: e.Name, text: b.String()}
}

// assignable are the agents A lists: none closing (HandTask refuses one), and an orphan only while
// it runs (with its profile gone, a stopped one cannot be started).
func assignable(entries []AgentEntry) []AgentEntry {
	var out []AgentEntry
	for _, e := range entries {
		if !e.Closing && (!e.Orphan || e.Running) {
			out = append(out, e)
		}
	}
	return out
}

// assign is A: the task under the cursor, in a store taking writes here, offered to AutoDoc's
// agents in the picker. Off a task, or on a store not claimed, it does nothing: the action row
// offers it only where it applies.
func (t *todos) assign() {
	s, x := t.cursor()
	if x == nil || t.readOnly(s) {
		return
	}
	agents := assignable(t.c.AgentEntries())
	if len(agents) == 0 {
		t.c.Notify("todos: no AutoDoc agent to assign to: add one in the agent profiles")
		return
	}
	t.assigning = &todoAssign{store: s, task: x, ws: t.c.Ticket(nil, TicketWorkspace)}
	t.assignPick.setAll(agents)
	t.assignPick.open()
}

// assignTo is the agent chosen: the Instructions: line is asked for, emptied.
func (t *todos) assignTo(e AgentEntry) {
	if t.assigning == nil {
		return
	}
	t.assigning.agent = e
	t.c.Set("App.todoAssignTitle", "assign to "+e.Name)
	t.c.Set("App.todoAssignText", "\x00") // moved away first, so an empty field twice still empties it
	t.c.Set("App.todoAssignText", "")
	t.c.Open("todoAssign")
}

// assignSet is the Instructions: line accepted: the task is assigned to the agent chosen, then
// handed to it. Esc closes the line without a call, so nothing is written.
func (t *todos) assignSet(text string) {
	t.c.Close("todoAssign")
	a := t.assigning
	t.assigning = nil
	if a == nil || a.agent.Ref.Codename == "" {
		return
	}
	instruction := strings.TrimSpace(text)
	if instruction == "" {
		instruction = assignDefault
	}
	assignee := assigneePrefix + a.agent.Ref.Codename
	s, x := a.store, a.task
	from := str(x, "status")
	t.write(s, "todos.assign", []any{s.at, str(x, "id"), str(x, "rev"), assignee, instruction}, func(v any) {
		if !a.ws.Current() { // the assignment stands; another workspace's agent never hears of it
			t.c.Notify(fmt.Sprintf("todos: assigned to %s, but not delivered (the workspace changed before the assign landed): "+
				"A on the task in its own workspace delivers it", assignee))
			return
		}
		task := asMap(v)
		if to := str(task, "status"); to != "" && to != from { // an open task is in progress now (D1)
			t.follow = todoFollow{store: s.id, id: str(x, "id"), bucket: to,
				waiting: map[string]bool{bucketKey(s.id, from): true, bucketKey(s.id, to): true}}
			t.open[bucketKey(s.id, to)] = true
		}
		t.handOver(a, s, task, assignee, instruction)
	})
}

// handOver gives the task, as the assign wrote it, to the agent through HandTask. Its file is where
// the assign left it (in-progress/ once an open task is assigned), so the path is the answer's, not
// the row's.
func (t *todos) handOver(a *todoAssign, s *todoStore, task map[string]any, assignee, instruction string) {
	ref := TaskRef{Title: str(task, "title"), Path: filepath.Join(s.path, filepath.FromSlash(str(task, "path"))), ID: str(task, "id")}
	name := a.agent.Name
	done := func(err error) { t.handedOver(name, assignee, err) }
	if err := t.c.HandTask(AgentRef{Codename: a.agent.Ref.Codename}, ref, instruction, done); err != nil {
		done(err)
	}
}

// handedOver says how the hand-over went. The assignment stands either way.
func (t *todos) handedOver(name, assignee string, err error) {
	if err == nil {
		t.c.Notify("todos: assigned to " + name + ", and handed to its terminal")
		return
	}
	why := err.Error()
	if errors.Is(err, ErrAgentCancelled) {
		why = "you didn't start it"
	}
	t.c.Notify(fmt.Sprintf("todos: assigned to %s, but not delivered (%s): start the agent, then A on the task to deliver it again",
		assignee, why))
}
