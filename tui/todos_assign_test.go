package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// hearingAgent is an agent that writes every line typed to it into heard-<its first argument> in
// dir, so a cell reads what HandTask delivered without reading a terminal's screen.
func hearingAgent(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hearing-agent")
	script := "#!/bin/sh\necho \"started $1\"\nwhile IFS= read -r line; do printf '%s\\n' \"$line\" >> " +
		shellQuote(dir) + "/heard-\"$1\"; done\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// heard is what agent name has been told, line by line.
func heard(dir, name string) []string {
	b, _ := os.ReadFile(filepath.Join(dir, "heard-"+name))
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// taskIn is the task titled title in bucket of kb's store, if it is there.
func taskIn(t *testing.T, d *daemon, bucket, title string) (todo.Task, bool) {
	t.Helper()
	l, err := d.todos.List(context.Background(), kbTodos, todo.ListOpts{Buckets: []string{bucket}})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range l.Tasks {
		if x.Title == title {
			return x, true
		}
	}
	return todo.Task{}, false
}

// TestTodosAssignHandsTheTaskToAnAutoDocAgent: A on a task opens a picker of AutoDoc's own agents,
// then the Instructions: line; Enter assigns the task to autodoc:<codename> (an open one is in
// progress now, the cursor following it) and hands it to the agent through HandTask, at the path
// the assign left the file at. Dismissing the picker or the line writes nothing. A stopped agent is
// asked about first: Yes starts it and delivers; declining leaves the task assigned, not delivered,
// and says how to deliver it again. A running agent is handed the task without a question.
func TestTodosAssignHandsTheTaskToAnAutoDocAgent(t *testing.T) {
	heardDir := t.TempDir()
	agent := hearingAgent(t, heardDir)
	d, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{
		{Name: "Scribe", Codename: "scribe", Command: shellQuote(agent) + " scribe"},
		{Name: "Critic", Codename: "critic", Command: shellQuote(agent) + " critic"},
	}, "")
	tasks := seedTodos(t, d, true, todo.Spec{Title: "Draft the guide"}, todo.Spec{Title: "Edit the links"},
		todo.Spec{Title: "Fix the typos"})
	openTodos(t, r, "· ws kb", '1')
	r.s.WaitForText(t, "3  Fix the typos")
	r.keys(t, key('j'))
	r.s.WaitForText(t, "## Draft the guide")
	unassigned := func(what string) {
		t.Helper()
		time.Sleep(200 * time.Millisecond)
		if x, ok := taskIn(t, d, "open", "Draft the guide"); !ok || x.Assignee != "" {
			t.Fatalf("%s wrote the task: %+v (open %v)", what, x, ok)
		}
	}

	// The picker lists AutoDoc's agents, stopped as they are; Esc dismisses it, and nothing is written.
	r.keys(t, key('A'))
	r.s.WaitForText(t, "assign to an agent")
	r.s.WaitForText(t, "Scribe  (scribe)")
	r.s.WaitForText(t, "Critic  (critic)")
	r.s.WaitForText(t, "stopped")
	r.keys(t, esc())
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "assign to an agent") })
	unassigned("a dismissed picker")

	// Chosen, the Instructions: line; Esc there writes nothing either.
	r.keys(t, key('A'))
	r.s.WaitForText(t, "Scribe  (scribe)")
	r.keys(t, enter())
	r.s.WaitForText(t, "assign to Scribe")
	r.s.WaitForText(t, "Instructions:")
	r.keys(t, esc())
	r.s.WaitFor(t, "the line closed", func(sc string) bool { return !strings.Contains(sc, "assign to Scribe") })
	unassigned("a cancelled Instructions: line")

	// An empty line: "Task assigned to you."; Scribe is stopped, so it is asked about, and Yes starts
	// it and hands it the task at its in-progress path (D1 moved the file there).
	r.keys(t, key('A'))
	r.s.WaitForText(t, "Scribe  (scribe)")
	r.keys(t, enter())
	r.s.WaitForText(t, "Instructions:")
	r.keys(t, enter())
	r.s.WaitForText(t, "start the agent?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "Scribe told", func(string) bool { return len(heard(heardDir, "scribe")) >= 4 })
	x, ok := taskIn(t, d, "in-progress", "Draft the guide")
	if !ok || x.Assignee != "autodoc:scribe" {
		t.Fatalf("after A the task is %+v (in progress %v), want in progress for autodoc:scribe", x, ok)
	}
	got := heard(heardDir, "scribe")
	file := strings.TrimPrefix(got[1], "File: ")
	if got[0] != "Task: Draft the guide" || got[2] != "ID: "+tasks[0].ID || got[3] != "Instruction: "+assignDefault {
		t.Fatalf("Scribe heard %q", got)
	}
	// the workspace kb is the daemon's /kb: the store is its .todo-list, on the todos' own disk
	if want := "/kb/.todo-list/in-progress/" + tasks[0].ID + ".md"; file != want || x.Path != "in-progress/"+tasks[0].ID+".md" {
		t.Fatalf("Scribe was handed %q (the task at %q), want %q: the path the assign returned", file, x.Path, want)
	}
	if _, err := d.todoDisk.ReadFile(file); err != nil {
		t.Fatalf("the path handed over is not the task's file: %v", err)
	}
	r.s.WaitFor(t, "the delivery told", func(string) bool { return r.noticed("assigned to Scribe, and handed to its terminal") })
	r.s.WaitForText(t, "status: in-progress") // the cursor followed the task

	// Critic is stopped, and declining to start it leaves the task assigned, not delivered.
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoTree", "forceActiveFocus")); return true })
	r.keys(t, key('1'), key('j'))
	r.s.WaitForText(t, "## Edit the links")
	r.keys(t, key('A'))
	r.s.WaitForText(t, "Critic  (critic)")
	r.keys(t, decltest.Type("crit")...)
	r.s.WaitForText(t, "agents (1 of 2)")
	r.keys(t, enter())
	r.s.WaitForText(t, "assign to Critic")
	r.keys(t, enter())
	r.s.WaitForText(t, "start the agent?")
	r.keys(t, esc())
	r.s.WaitFor(t, "the decline told", func(string) bool {
		return r.noticed("assigned to autodoc:critic, but not delivered (you didn't start it): start the agent, then A on the task")
	})
	if x, ok := taskIn(t, d, "in-progress", "Edit the links"); !ok || x.Assignee != "autodoc:critic" {
		t.Fatalf("a declined start rolled the assignment back: %+v (in progress %v)", x, ok)
	}
	if _, err := os.Stat(filepath.Join(heardDir, "heard-critic")); err == nil {
		t.Fatal("Critic was told the task though it was not started")
	}

	// Scribe runs now: a typed instruction, and no question.
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoTree", "forceActiveFocus")); return true })
	r.keys(t, key('1'), key('j'))
	r.s.WaitForText(t, "## Fix the typos")
	r.keys(t, key('A'))
	r.s.WaitForText(t, "running")
	r.keys(t, decltest.Type("scribe")...) // the picker's cursor stays on the agent last chosen: name the one wanted
	r.s.WaitForText(t, "agents (1 of 2)")
	r.keys(t, enter())
	r.s.WaitForText(t, "assign to Scribe")
	r.keys(t, decltest.Type("Read the ADR first")...)
	r.keys(t, enter())
	r.s.WaitFor(t, "Scribe told again", func(string) bool { return len(heard(heardDir, "scribe")) >= 8 })
	if got := heard(heardDir, "scribe"); got[4] != "Task: Fix the typos" || got[7] != "Instruction: Read the ADR first" {
		t.Fatalf("Scribe heard %q", got)
	}
	if strings.Contains(r.s.String(), "start the agent?") {
		t.Fatal("a running agent was asked about")
	}
}

// TestAssignableAgents: the picker leaves out a closing instance (HandTask refuses one) and a stopped
// orphan (its profile gone, it cannot be started), and keeps the order AgentEntries gives.
func TestAssignableAgents(t *testing.T) {
	entries := []AgentEntry{
		{Ref: AgentRef{Codename: "stopped"}},
		{Ref: AgentRef{Codename: "closing", Instance: "1"}, Running: true, Closing: true},
		{Ref: AgentRef{Codename: "running", Instance: "2"}, Running: true},
		{Ref: AgentRef{Codename: "orphan-running", Instance: "3"}, Running: true, Orphan: true},
		{Ref: AgentRef{Codename: "orphan-stopped"}, Orphan: true},
	}
	var got []string
	for _, e := range assignable(entries) {
		got = append(got, e.Ref.Codename)
	}
	if want := []string{"stopped", "running", "orphan-running"}; !slices.Equal(got, want) {
		t.Fatalf("assignable = %v, want %v", got, want)
	}
}

// agentCore is a scripted daemon with AutoDoc's agents: AgentEntries lists entries, and HandTask
// records each hand-over, refusing at once with refuse when it is set. switchWorkspace moves the
// workspace on: a TicketWorkspace taken before it is stale.
type agentCore struct {
	*scriptedCore
	entries []AgentEntry
	handed  []handOverCall
	refuse  error
	wsGen   int
}

func (c *agentCore) switchWorkspace() { c.wsGen++ }
func (c *agentCore) Ticket(seq *uint64, scope TicketScope) Ticket {
	if seq != nil || scope != TicketWorkspace {
		return c.scriptedCore.Ticket(seq, scope)
	}
	gen := c.wsGen
	return Ticket{current: func() bool { return c.wsGen == gen }}
}

type handOverCall struct {
	to          AgentRef
	task        TaskRef
	instruction string
	done        func(error)
}

func (c *agentCore) AgentEntries() []AgentEntry { return c.entries }
func (c *agentCore) HandTask(to AgentRef, task TaskRef, instruction string, done func(error)) error {
	if c.refuse != nil {
		return c.refuse
	}
	c.handed = append(c.handed, handOverCall{to, task, instruction, done})
	return nil
}

// assigningStore is a claimed store whose open task t1 todos.assign moves to in-progress, as D1
// does; assigned records each todos.assign's params.
func assigningStore(entries ...AgentEntry) (*agentCore, *[][]any) {
	s := scriptedStore(true)
	var assigned [][]any
	s.answers["todos.assign"] = func(p []any) (any, error) {
		assigned = append(assigned, p)
		return map[string]any{"id": "t1", "rev": "r2", "title": "Ship", "status": "in-progress", "path": "in-progress/t1.md",
			"assignee": p[3]}, nil
	}
	return &agentCore{scriptedCore: s, entries: entries}, &assigned
}

// TestTodosAssignWritesThenHandsOverAtTheReturnedPath: the assign is written first (autodoc:
// <codename>, the instruction its reason, an empty one "Task assigned to you."), then the task is
// handed over at the path the assign returned, the store's root joined: in-progress/, where D1 left
// the file, not open/, where the row had it. The current incarnation is HandTask's to resolve.
func TestTodosAssignWritesThenHandsOverAtTheReturnedPath(t *testing.T) {
	c, assigned := assigningStore(AgentEntry{Ref: AgentRef{Codename: "scribe", Instance: "7"}, Name: "Scribe", Running: true})
	v := newTodos(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	if !slices.Contains(c.opened, "agentAssignPicker") || c.set["App.agentAssignStatus"] != "agents (1 of 1)" {
		t.Fatalf("A opened %v, listing %v", c.opened, c.set["App.agentAssignStatus"])
	}
	v.assignPick.accept(0, "")
	if c.opened[len(c.opened)-1] != "todoAssign" || c.set["App.todoAssignText"] != "" || c.set["App.todoAssignTitle"] != "assign to Scribe" {
		t.Fatalf("the agent chosen: opened %v, text %q, title %v", c.opened, c.set["App.todoAssignText"], c.set["App.todoAssignTitle"])
	}
	v.assignSet("   ")
	if len(*assigned) != 1 {
		t.Fatalf("todos.assign called %d times", len(*assigned))
	}
	if p := (*assigned)[0]; p[1] != "t1" || p[2] != "r1" || p[3] != "autodoc:scribe" || p[4] != assignDefault {
		t.Fatalf("todos.assign params %v", p)
	}
	if len(c.handed) != 1 {
		t.Fatalf("handed over %d times", len(c.handed))
	}
	h := c.handed[0]
	if h.to != (AgentRef{Codename: "scribe"}) || h.instruction != assignDefault ||
		h.task != (TaskRef{Title: "Ship", Path: "/kb/.todo-list/in-progress/t1.md", ID: "t1"}) {
		t.Fatalf("handed %+v", h)
	}
	h.done(nil)
	h.done(ErrAgentCancelled) // as a second A's would: each says how it went
	if len(c.notes) != 2 || c.notes[0] != "todos: assigned to Scribe, and handed to its terminal" ||
		!strings.Contains(c.notes[1], "assigned to autodoc:scribe, but not delivered (you didn't start it)") {
		t.Fatalf("notes %q", c.notes)
	}

	// a typed instruction is the reason and the instruction; HandTask's refusal at once is told, and
	// the assignment stands (nothing undoes it)
	c.refuse = ErrAgentUnavailable
	v.assign()
	v.assignPick.accept(0, "")
	v.assignSet("  Read the ADR first ")
	if p := (*assigned)[1]; p[4] != "Read the ADR first" {
		t.Fatalf("todos.assign params %v", p)
	}
	if last := c.notes[len(c.notes)-1]; !strings.Contains(last, "not delivered ("+ErrAgentUnavailable.Error()+")") {
		t.Fatalf("a refused hand-over: %q", last)
	}
	if slices.Contains(c.calls, "todos.status") {
		t.Fatalf("an assignment was undone: %v", c.calls)
	}
}

// TestTodosAssignIsRefusedWithoutATaskAnAgentOrWrites: A off a task row, or on a store not claimed,
// does nothing and says nothing (the action row offers it on neither); with no AutoDoc agent to list
// it says how to add one. None opens the picker. A dismissed picker has chosen no agent: nothing is
// written.
func TestTodosAssignIsRefusedWithoutATaskAnAgentOrWrites(t *testing.T) {
	c, assigned := assigningStore(AgentEntry{Ref: AgentRef{Codename: "scribe"}, Name: "Scribe"})
	v := newTodos(c)
	v.list()
	ix := openSection(t, v, "ws:kb", "open")
	v.moved(ix) // the section's row, not a task's
	v.assign()
	if c.set["App.todosStatus"] != "" || len(c.notes) != 0 || slices.Contains(c.opened, "agentAssignPicker") {
		t.Fatalf("off a task: status %q, notes %q, opened %v", c.set["App.todosStatus"], c.notes, c.opened)
	}
	v.moved(tuidecl.Index{Row: 0, Parent: &ix})
	v.assign()
	v.assignPick.dismiss()
	v.assignSet("") // as a stray App.todoAssignSet after the dismissal would
	if len(*assigned) != 0 {
		t.Fatalf("a dismissed picker wrote %v", *assigned)
	}

	none, _ := assigningStore(AgentEntry{Ref: AgentRef{Codename: "gone"}, Closing: true})
	v = newTodos(none)
	v.list()
	onTask(t, v, "open")
	v.assign()
	if len(none.notes) != 1 || none.notes[0] != "todos: no AutoDoc agent to assign to: add one in the agent profiles" ||
		slices.Contains(none.opened, "agentAssignPicker") {
		t.Fatalf("no agent: notes %q, opened %v", none.notes, none.opened)
	}

	ro := &agentCore{scriptedCore: scriptedStore(false), entries: []AgentEntry{{Ref: AgentRef{Codename: "scribe"}, Name: "Scribe"}}}
	v = newTodos(ro)
	v.list()
	onTask(t, v, "open")
	v.assign()
	if len(ro.notes) != 0 || len(ro.failed) != 0 || slices.Contains(ro.opened, "agentAssignPicker") {
		t.Fatalf("read-only: notes %q, failed %q, opened %v", ro.notes, ro.failed, ro.opened)
	}
}

// TestTodosAssignAnsweredAfterAWorkspaceSwitchIsHandedToNoOne: HandTask delivers to the workspace in
// use, so an assign whose answer lands after a switch is not handed over: the assignment stands,
// and the notice says to deliver it from the task's own workspace. Answered late in the same
// workspace, it is handed over as ever.
func TestTodosAssignAnsweredAfterAWorkspaceSwitchIsHandedToNoOne(t *testing.T) {
	for _, switched := range []bool{false, true} {
		c, assigned := assigningStore(AgentEntry{Ref: AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true})
		v := newTodos(c)
		v.list()
		onTask(t, v, "open")
		v.assign()
		v.assignPick.accept(0, "")
		c.deferred = true
		v.assignSet("")
		if len(*assigned) != 1 || len(c.pending) != 1 {
			t.Fatalf("switched %v: assigned %v, answers held %d", switched, *assigned, len(c.pending))
		}
		if switched {
			c.switchWorkspace()
		}
		c.deliver(0) // the assign's answer, late
		switch {
		case switched && (len(c.handed) != 0 || len(c.notes) != 1 ||
			!strings.Contains(c.notes[0], "assigned to autodoc:scribe, but not delivered (the workspace changed before the assign landed)")):
			t.Fatalf("after a switch: handed %+v, notes %q", c.handed, c.notes)
		case !switched && (len(c.handed) != 1 || c.handed[0].task.Path != "/kb/.todo-list/in-progress/t1.md"):
			t.Fatalf("same workspace, answered late: handed %+v", c.handed)
		}
	}
}
