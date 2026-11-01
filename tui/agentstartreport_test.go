package tui

import (
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// A registration the daemon refuses is shown in the agent's own tab, not only in a toast: the
// tab's start answers the refusal (the terminal writes it on its screen), the tab is the one
// selected, and it stays stopped; a toast says it too. Before, the tab stayed empty and the toast
// was the only trace (cloud-strife's finding 4).
func TestARefusedRegistrationIsShownInItsTab(t *testing.T) {
	a, c, w := crewAgents(t, oneTwo)
	c.panel = &modelPanel{owner: c, starts: true}
	w.d.refuse = &golibrpc.Error{Code: crew.CodeCodenameBusy, Message: "the codename already has a live session"}
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if instance.running || c.panel.tabs[0].Running {
		t.Fatalf("a refused registration started the agent: running %v", instance.running)
	}
	if len(c.panel.reported) != 1 || !strings.Contains(c.panel.reported[0], "the codename already has a live session") {
		t.Fatalf("the agent's tab showed %q, want the daemon's refusal", c.panel.reported)
	}
	if c.panel.active != instance.tab {
		t.Fatalf("the selected tab %q, want the refused agent's %q", c.panel.active, instance.tab)
	}
	if len(c.notes) == 0 || !strings.Contains(c.notes[len(c.notes)-1], "the codename already has a live session") {
		t.Fatalf("notices %v, want the refusal said", c.notes)
	}
}

// A profile command whose program isn't on the agent's PATH (cloud-strife's claude-work, a bash
// alias: AutoDoc runs the command with /bin/sh -c, which reads no alias) is refused before anything
// is set up or registered: no crew.register, the tab and a toast naming the word and why an alias
// isn't seen. A command in shell syntax (an assignment, a pipe, a substitution) can't be judged by
// its first word, so it isn't.
func TestAProfileCommandNotOnPathIsRefusedBeforeItsSession(t *testing.T) {
	a, c, w := crewAgents(t, `[{"name":"Sephiroth","command":"claude-work","codename":"sephiroth"},{"name":"Shell","command":"FOO=1 claude-work","codename":"shell"}]`)
	c.panel = &modelPanel{owner: c, starts: true}
	a.commandCheck = commandOnPath // as a window's agents are
	p, _ := a.profile("sephiroth")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := calledTimes(w, "crew.register"); n != 0 {
		t.Fatalf("a command not on PATH was registered %d times, want none", n)
	}
	if instance.running || instance.starting {
		t.Fatalf("a command not on PATH: running %v starting %v", instance.running, instance.starting)
	}
	if len(c.panel.reported) != 1 || !strings.Contains(c.panel.reported[0], "claude-work") || !strings.Contains(c.panel.reported[0], "alias") {
		t.Fatalf("the agent's tab showed %q, want claude-work named and the alias said", c.panel.reported)
	}
	if len(c.notes) == 0 || !strings.Contains(c.notes[len(c.notes)-1], "claude-work") {
		t.Fatalf("notices %v, want claude-work named", c.notes)
	}
	reported := len(c.panel.reported)
	shell, _ := a.profile("shell")
	if _, err := a.startInstance(shell); err != nil {
		t.Fatal(err)
	}
	if len(c.panel.reported) != reported && strings.Contains(c.panel.reported[len(c.panel.reported)-1], "isn't a program on") {
		t.Fatalf("a command in shell syntax was judged by its first word: %q", c.panel.reported)
	}
}
