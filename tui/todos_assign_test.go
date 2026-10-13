package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	tasks := seedTodos(t, d, true, todo.Spec{Title: "Write the guide"}, todo.Spec{Title: "Check the links"},
		todo.Spec{Title: "Fix the typos"})
	openTodos(t, r, "· ws kb", '1')
	r.s.WaitForText(t, "3  Fix the typos")
	r.keys(t, key('j'))
	r.s.WaitForText(t, "## Write the guide")
	unassigned := func(what string) {
		t.Helper()
		time.Sleep(200 * time.Millisecond)
		if x, ok := taskIn(t, d, "open", "Write the guide"); !ok || x.Assignee != "" {
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
	x, ok := taskIn(t, d, "in-progress", "Write the guide")
	if !ok || x.Assignee != "autodoc:scribe" {
		t.Fatalf("after A the task is %+v (in progress %v), want in progress for autodoc:scribe", x, ok)
	}
	got := heard(heardDir, "scribe")
	file := strings.TrimPrefix(got[1], "File: ")
	if got[0] != "Task: Write the guide" || got[2] != "ID: "+tasks[0].ID || got[3] != "Instruction: "+assignDefault {
		t.Fatalf("Scribe heard %q", got)
	}
	if !filepath.IsAbs(file) || !strings.HasSuffix(file, "/in-progress/"+tasks[0].ID+".md") {
		t.Fatalf("Scribe was handed %q, want the task's absolute path under in-progress/", file)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("the path handed over is not the task's file: %v", err)
	}
	r.s.WaitFor(t, "the delivery told", func(string) bool { return r.noticed("assigned to Scribe, and handed to its terminal") })
	r.s.WaitForText(t, "status: in-progress") // the cursor followed the task

	// Critic is stopped, and declining to start it leaves the task assigned, not delivered.
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoTree", "forceActiveFocus")); return true })
	r.keys(t, key('1'), key('j'))
	r.s.WaitForText(t, "## Check the links")
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
	if x, ok := taskIn(t, d, "in-progress", "Check the links"); !ok || x.Assignee != "autodoc:critic" {
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

// TestTodosAssignIsRefusedWithoutATaskOrAnAgent: A off a task row says what it needs; with no
// AutoDoc agent it says how to add one; on a store not claimed it is the store's read-only notice.
// None opens the picker.
func TestTodosAssignIsRefusedWithoutATaskOrAnAgent(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Ship it"})
	r := attached(t, d)
	openTodos(t, r, "· ws kb", '1')
	r.s.WaitForText(t, "1  Ship it")
	r.keys(t, key('A')) // on the Open section's row
	r.s.WaitForText(t, "A assigns a task: put the cursor on one")
	r.keys(t, key('j'))
	r.s.WaitForText(t, "## Ship it")
	r.keys(t, key('A'))
	r.s.WaitFor(t, "no agent", func(string) bool { return r.noticed("no AutoDoc agent to assign to: add one in the agent profiles") })
	if strings.Contains(r.s.String(), "assign to an agent") {
		t.Fatal("A opened the picker with no agent to list")
	}

	ro := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, ro, false, todo.Spec{Title: "Read only"})
	rr := attached(t, ro)
	openTodos(t, rr, "· ws kb  (read-only", '1')
	rr.s.WaitForText(t, "1  Read only")
	rr.keys(t, key('j'))
	rr.s.WaitForText(t, "## Read only")
	rr.keys(t, key('A'))
	rr.waitNotice(t, "read-only in AutoDoc until the cutover")
	if strings.Contains(rr.s.String(), "assign to an agent") {
		t.Fatal("A on a read-only store opened the picker")
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
