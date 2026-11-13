package todos

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget/terminal"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
)

// TestAssignableAgents: the picker leaves out a closing instance (HandTask refuses one) and a stopped
// orphan (its profile gone, it cannot be started), and keeps the order AgentEntries gives.
//
// Moved from tui/todos_assign_test.go with its name and asserts unchanged.
func TestAssignableAgents(t *testing.T) {
	entries := []feature.AgentEntry{
		{Ref: feature.AgentRef{Codename: "stopped"}},
		{Ref: feature.AgentRef{Codename: "closing", Instance: "1"}, Running: true, Closing: true},
		{Ref: feature.AgentRef{Codename: "running", Instance: "2"}, Running: true},
		{Ref: feature.AgentRef{Codename: "orphan-running", Instance: "3"}, Running: true, Orphan: true},
		{Ref: feature.AgentRef{Codename: "orphan-stopped"}, Orphan: true},
	}
	var got []string
	for _, e := range Assignable(entries) {
		got = append(got, e.Ref.Codename)
	}
	if want := []string{"stopped", "running", "orphan-running"}; !slices.Equal(got, want) {
		t.Fatalf("Assignable = %v, want %v", got, want)
	}
}

// agentCore is a scripted daemon with AutoDoc's agents: AgentEntries lists entries, and HandTask
// records each hand-over, refusing at once with refuse when it is set. switchWorkspace moves the
// workspace on: a TicketWorkspace taken before it is stale.
type agentCore struct {
	*featuretest.ScriptedCore
	entries []feature.AgentEntry
	handed  []handOverCall
	refuse  error
	wsGen   int
}

func (c *agentCore) switchWorkspace() { c.wsGen++ }
func (c *agentCore) Ticket(seq *uint64, scope feature.TicketScope) feature.Ticket {
	if seq != nil || scope != feature.TicketWorkspace {
		return c.ScriptedCore.Ticket(seq, scope)
	}
	gen := c.wsGen
	return feature.NewTicket(func() bool { return c.wsGen == gen })
}

type handOverCall struct {
	to          feature.AgentRef
	task        feature.TaskRef
	instruction string
	done        func(error)
}

func (c *agentCore) AgentEntries() []feature.AgentEntry { return c.entries }
func (c *agentCore) HandTask(to feature.AgentRef, task feature.TaskRef, instruction string, done func(error)) error {
	if c.refuse != nil {
		return c.refuse
	}
	c.handed = append(c.handed, handOverCall{to, task, instruction, done})
	return nil
}

// assigningStore is a claimed store whose open task t1 todos.assign moves to in-progress, as D1
// does; assigned records each todos.assign's params.
func assigningStore(entries ...feature.AgentEntry) (*agentCore, *[][]any) {
	s := scriptedStore(true)
	var assigned [][]any
	s.Answers["todos.assign"] = func(p []any) (any, error) {
		assigned = append(assigned, p)
		return map[string]any{"id": "t1", "rev": "r2", "title": "Ship", "status": "in-progress", "path": "in-progress/t1.md",
			"assignee": p[3]}, nil
	}
	return &agentCore{ScriptedCore: s, entries: entries}, &assigned
}

// TestTodosAssignWritesThenHandsOverAtTheReturnedPath: the assign is written first (autodoc:
// <codename>, the instruction its reason, an empty one "Task assigned to you."), then the task is
// handed over at the path the assign returned, the store's root joined: in-progress/, where D1 left
// the file, not open/, where the row had it. The current incarnation is HandTask's to resolve.
//
// Moved from tui/todos_assign_test.go with its name and asserts unchanged.
func TestTodosAssignWritesThenHandsOverAtTheReturnedPath(t *testing.T) {
	c, assigned := assigningStore(feature.AgentEntry{Ref: feature.AgentRef{Codename: "scribe", Instance: "7"}, Name: "Scribe", Running: true})
	v := New(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	if !slices.Contains(c.Opened, "agentAssignPicker") || c.Sets["App.agentAssignStatus"] != "agents (1 of 1)" {
		t.Fatalf("A opened %v, listing %v", c.Opened, c.Sets["App.agentAssignStatus"])
	}
	v.assignPick.Accept(0, "")
	if c.Opened[len(c.Opened)-1] != "todoAssign" || c.Sets["App.todoAssignText"] != "" || c.Sets["App.todoAssignTitle"] != "assign to Scribe" {
		t.Fatalf("the agent chosen: opened %v, text %q, title %v", c.Opened, c.Sets["App.todoAssignText"], c.Sets["App.todoAssignTitle"])
	}
	v.assignSet("   ")
	if len(*assigned) != 1 {
		t.Fatalf("todos.assign called %d times", len(*assigned))
	}
	if p := (*assigned)[0]; p[1] != "t1" || p[2] != "r1" || p[3] != "autodoc:scribe" || p[4] != AssignDefault {
		t.Fatalf("todos.assign params %v", p)
	}
	if len(c.handed) != 1 {
		t.Fatalf("handed over %d times", len(c.handed))
	}
	h := c.handed[0]
	if h.to != (feature.AgentRef{Codename: "scribe"}) || h.instruction != AssignDefault ||
		h.task != (feature.TaskRef{Title: "Ship", Path: taskFile("/kb/.todo-list/in-progress/t1.md"), ID: "t1"}) {
		t.Fatalf("handed %+v", h)
	}
	h.done(nil)
	h.done(agents.ErrAgentCancelled)             // as a second A's would: each says how it went
	h.done(terminal.ErrTerminalPasteUnbracketed) // a program that takes no bracketed paste got nothing
	if len(c.Notes) != 3 || c.Notes[0] != "todos: assigned to Scribe, and sent to it" ||
		!strings.Contains(c.Notes[1], "assigned to autodoc:scribe, but not delivered (you didn't start it)") ||
		c.Notes[2] != "todos: assigned to Scribe, but not handed over: its program takes no bracketed paste; open its panel and paste the task" {
		t.Fatalf("notes %q", c.Notes)
	}

	// a typed instruction is the reason and the instruction; HandTask's refusal at once is told, and
	// the assignment stands (nothing undoes it)
	c.refuse = agents.ErrAgentUnavailable
	v.showTab(0) // the task followed to In progress; this store's answers keep it in Open
	v.assign()
	v.assignPick.Accept(0, "")
	v.assignSet("  Read the ADR first ")
	if p := (*assigned)[1]; p[4] != "Read the ADR first" {
		t.Fatalf("todos.assign params %v", p)
	}
	if last := c.Notes[len(c.Notes)-1]; !strings.Contains(last, "not delivered ("+agents.ErrAgentUnavailable.Error()+")") {
		t.Fatalf("a refused hand-over: %q", last)
	}
	if slices.Contains(c.Calls, "todos.status") {
		t.Fatalf("an assignment was undone: %v", c.Calls)
	}
}

// TestTodosAssignIsRefusedWithoutATaskAnAgentOrWrites: A off a task row, or on a store not claimed,
// does nothing and says nothing (the action row offers it on neither); with no AutoDoc agent to list
// it says how to add one. None opens the picker. A dismissed picker has chosen no agent: nothing is
// written.
//
// Moved from tui/todos_assign_test.go with its name and asserts unchanged.
func TestTodosAssignIsRefusedWithoutATaskAnAgentOrWrites(t *testing.T) {
	c, assigned := assigningStore(feature.AgentEntry{Ref: feature.AgentRef{Codename: "scribe"}, Name: "Scribe"})
	v := New(c)
	v.list()
	v.showTab(1) // In progress: no task under the cursor
	v.assign()
	if strings.Contains(fmt.Sprint(c.Sets["App.todosStatus"]), "assign") || len(c.Notes) != 0 || slices.Contains(c.Opened, "agentAssignPicker") {
		t.Fatalf("off a task: status %q, notes %q, opened %v", c.Sets["App.todosStatus"], c.Notes, c.Opened)
	}
	v.showTab(0)
	v.assign()
	v.assignPick.Dismiss()
	v.assignSet("") // as a stray App.todoAssignSet after the dismissal would
	if len(*assigned) != 0 {
		t.Fatalf("a dismissed picker wrote %v", *assigned)
	}

	none, _ := assigningStore(feature.AgentEntry{Ref: feature.AgentRef{Codename: "gone"}, Closing: true})
	v = New(none)
	v.list()
	onTask(t, v, "open")
	v.assign()
	if len(none.Notes) != 1 || none.Notes[0] != "todos: no AutoDoc agent to assign to: add one in the agent profiles" ||
		slices.Contains(none.Opened, "agentAssignPicker") {
		t.Fatalf("no agent: notes %q, opened %v", none.Notes, none.Opened)
	}

	ro := &agentCore{ScriptedCore: scriptedStore(false), entries: []feature.AgentEntry{{Ref: feature.AgentRef{Codename: "scribe"}, Name: "Scribe"}}}
	v = New(ro)
	v.list()
	onTask(t, v, "open")
	v.assign()
	if len(ro.Notes) != 0 || len(ro.Failures) != 0 || slices.Contains(ro.Opened, "agentAssignPicker") {
		t.Fatalf("read-only: notes %q, failed %q, opened %v", ro.Notes, ro.Failures, ro.Opened)
	}
}

// TestTodosAssignAnsweredAfterAWorkspaceSwitchIsHandedToNoOne: HandTask delivers to the workspace in
// use, so an assign whose answer lands after a switch is not handed over: the assignment stands,
// and the notice says to deliver it from the task's own workspace. Answered late in the same
// workspace, it is handed over as ever.
//
// Moved from tui/todos_assign_test.go with its name and asserts unchanged.
func TestTodosAssignAnsweredAfterAWorkspaceSwitchIsHandedToNoOne(t *testing.T) {
	for _, switched := range []bool{false, true} {
		c, assigned := assigningStore(feature.AgentEntry{Ref: feature.AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true})
		v := New(c)
		v.list()
		onTask(t, v, "open")
		v.assign()
		v.assignPick.Accept(0, "")
		c.Deferred = true
		v.assignSet("")
		if len(*assigned) != 1 || len(c.Pending) != 1 {
			t.Fatalf("switched %v: assigned %v, answers held %d", switched, *assigned, len(c.Pending))
		}
		if switched {
			c.switchWorkspace()
		}
		c.Deliver(0) // the assign's answer, late
		switch {
		case switched && (len(c.handed) != 0 || len(c.Notes) != 1 ||
			!strings.Contains(c.Notes[0], "assigned to autodoc:scribe, but not delivered (the workspace changed before the assign landed)")):
			t.Fatalf("after a switch: handed %+v, notes %q", c.handed, c.Notes)
		case !switched && (len(c.handed) != 1 || c.handed[0].task.Path != taskFile("/kb/.todo-list/in-progress/t1.md")):
			t.Fatalf("same workspace, answered late: handed %+v", c.handed)
		}
	}
}

// A's answer that lands after the stores were listed again (the panel opened again, a label set, a
// store registered) is still the shown store's: the task is followed to In progress, which is read
// again, and Open no longer shows it. Listing made the store shown a new value, and the answer,
// compared by value, was dropped: no follow, no read.
//
// Moved from tui/todos_assign_test.go with its name and asserts unchanged.
func TestTodosAssignAnsweredAfterTheStoresAreListedAgainFollowsTheTask(t *testing.T) {
	c, assigned := assigningStore(feature.AgentEntry{Ref: feature.AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true})
	v := New(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	v.assignPick.Accept(0, "")
	c.Deferred = true
	v.assignSet("")
	if len(*assigned) != 1 || len(c.Pending) != 1 {
		t.Fatalf("the premise, the assign's answer held: assigned %v, held %d", *assigned, len(c.Pending))
	}
	assignAnswer := c.Pending[0]
	c.Pending = nil
	shown := v.cur
	v.list()                 // the stores listed again while the assign is out
	for len(c.Pending) > 0 { // the listing to its end: the stores, their labels, the workspace's store
		held := c.Pending
		c.Pending = nil
		for _, answer := range held {
			answer()
		}
	}
	if v.cur == shown || v.cur == nil || v.cur.id != shown.id {
		t.Fatalf("the premise, the same store listed again as a new value: %p %p", shown, v.cur)
	}
	c.Deferred = false
	before := len(c.Calls)
	assignAnswer() // then the assign's answer
	inProgress := tabOf("in-progress")
	if v.tab != inProgress || c.Sets["App.todoTab"] != inProgress {
		t.Fatalf("after the assign the panel is on tab %d (App.todoTab %v), want In progress (%d)", v.tab, c.Sets["App.todoTab"], inProgress)
	}
	read := false
	for _, call := range c.Calls[before:] {
		read = read || strings.HasPrefix(call, "todos.list") && strings.Contains(call, "in-progress")
	}
	if !read {
		t.Fatalf("In progress was not read after the assign's answer: %v", c.Calls[before:])
	}
}

// scopedAgentCore is agentCore in a window holding the crew scope scope ("" for none).
type scopedAgentCore struct {
	*agentCore
	scope string
}

func (c *scopedAgentCore) windowScope() string { return c.scope }

// TestTodosAssignCarriesTheWindowsScope: A assigns with the window's crew scope, so the window's
// assignment feed (crewtodos.go) leaves this assignment to the hand-over A makes itself; a window
// holding no scope assigns with none, as before.
//
// Moved from tui/todos_assign_test.go with its name and asserts unchanged.
func TestTodosAssignCarriesTheWindowsScope(t *testing.T) {
	for _, scope := range []string{"autodoc:0123456789abcdef", ""} {
		ac, assigned := assigningStore(feature.AgentEntry{Ref: feature.AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true})
		c := &scopedAgentCore{agentCore: ac, scope: scope}
		v := New(c)
		v.list()
		onTask(t, v, "open")
		v.assign()
		v.assignPick.Accept(0, "")
		v.assignSet("Go.")
		if len(*assigned) != 1 {
			t.Fatalf("scope %q: todos.assign called %d times", scope, len(*assigned))
		}
		p := (*assigned)[0]
		if scope != "" && (len(p) != 6 || p[5] != scope) || scope == "" && len(p) != 5 {
			t.Errorf("scope %q: todos.assign params %v", scope, p)
		}
		if len(ac.handed) != 1 {
			t.Errorf("scope %q: handed over %d times, want once, by A itself", scope, len(ac.handed))
		}
	}
}
