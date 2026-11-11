package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/todos"
)

// hearingAgent is an agent that takes bracketed pastes (mode 2004), as agent CLIs do, and writes
// every byte it is sent, its terminal raw, into heard-<its first argument> in dir: this test binary
// (hearingAgentMain), on every OS. It says "started" only once it takes bracketed pastes, so a test
// that waits for that line may hand a running instance a task: a running agent that doesn't take
// them yet refuses a delivery at once, and a "started" written first left a window where it did
// (seen under coverage on CI). Its program's folder is shellTempDir's, as a running program's file
// can't be removed on Windows; its profile's command is agentCall's.
func hearingAgent(t *testing.T, dir string) string {
	t.Helper()
	bin := shellTempDir(t, "autodoc-hearing-agent-")
	agent := standIn(t, bin, "hearing-agent")
	if err := os.WriteFile(filepath.Join(bin, "heard-dir"), []byte(dir), 0o600); err != nil {
		t.Fatal(err)
	}
	return agent
}

// heardRaw is every byte agent name has been sent.
func heardRaw(dir, name string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "heard-"+name))
	return string(b)
}

// heard is what agent name has been pasted, line by line, paste after paste.
func heard(dir, name string) []string {
	var lines []string
	for _, p := range strings.Split(heardRaw(dir, name), "\x1b[200~")[1:] {
		p, _, _ = strings.Cut(p, "\x1b[201~")
		lines = append(lines, strings.Split(p, "\n")...)
	}
	return lines
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
	d, r, _ := runSetupTUI(t, agentsFolder(t), []agents.AgentProfile{
		{Name: "Scribe", Codename: "scribe", Command: agentCall(agent, "scribe")},
		{Name: "Critic", Codename: "critic", Command: agentCall(agent, "critic")},
	}, "", nil, Options{})
	tasks := seedTodos(t, d, true, todo.Spec{Title: "Draft the guide"}, todo.Spec{Title: "Edit the links"},
		todo.Spec{Title: "Fix the typos"})
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "Fix the typos")
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
	if got[0] != "Task: Draft the guide" || got[2] != "ID: "+tasks[0].ID || got[3] != "Instruction: "+todos.AssignDefault {
		t.Fatalf("Scribe heard %q", got)
	}
	// the workspace kb is the daemon's /kb: the store is its .todo-list, on the todos' own disk
	if want := taskFile("/kb/.todo-list/in-progress/" + tasks[0].ID + ".md"); file != want || x.Path != "in-progress/"+tasks[0].ID+".md" {
		t.Fatalf("Scribe was handed %q (the task at %q), want %q: the path the assign returned", file, x.Path, want)
	}
	if _, err := d.todoDisk.ReadFile(file); err != nil {
		t.Fatalf("the path handed over is not the task's file: %v", err)
	}
	r.s.WaitFor(t, "the delivery told", func(string) bool {
		return r.noticed("assigned to Scribe, and sent to it")
	})
	// sent, with no key pressed (docs/decisions/todos.md#a-message-to-an-agent-is-sent-never-left-for-enter):
	// Scribe has no doorbell here (no crew session), so the base typed the task as one paste and
	// pressed Enter after it, its panel open and the keyboard in it; on this small screen it covers
	// the todos' detail: SPC g's toggle hides it, Scribe running on
	r.s.WaitFor(t, "the task's Enter", func(string) bool { return strings.HasSuffix(heardRaw(heardDir, "scribe"), "\x1b[201~\r") })
	if raw := heardRaw(heardDir, "scribe"); strings.Count(raw, "\x1b[200~") != 1 || strings.Count(raw, "\r") != 1 {
		t.Fatalf("Scribe was sent %q, want one paste and one Enter after it", raw)
	}
	agentShown := func() bool { return onLoop(r, func() bool { return r.h.panelOpen["agent"] }) }
	r.s.WaitFor(t, "the agent's panel open", func(string) bool { return agentShown() })
	r.s.WaitFor(t, "the keyboard in the agent", func(string) bool { return r.focused("agentView") })
	onLoop(r, func() bool { r.h.agents.ToggleAgent(); return true })
	r.s.WaitFor(t, "the agent's panel hidden", func(string) bool { return !agentShown() })
	r.s.WaitForText(t, "status: in-progress") // the cursor followed the task

	// Critic is stopped, and declining to start it leaves the task assigned, not delivered.
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoInProgressTable", "forceActiveFocus")); return true })
	r.keys(t, key('1'))
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
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoInProgressTable", "forceActiveFocus")); return true })
	r.keys(t, key('1'))
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
	// its hidden panel brought forward, and the task sent: one Enter after each of the two pastes
	r.s.WaitFor(t, "the agent's panel open again", func(string) bool { return agentShown() && r.focused("agentView") })
	r.s.WaitFor(t, "the second task's Enter", func(string) bool { return strings.Count(heardRaw(heardDir, "scribe"), "\x1b[201~\r") == 2 })
	if raw := heardRaw(heardDir, "scribe"); strings.Count(raw, "\r") != 2 {
		t.Fatalf("Scribe was sent %q, want an Enter after each task and no other", raw)
	}
	if strings.Contains(r.s.String(), "start the agent?") {
		t.Fatal("a running agent was asked about")
	}
}
