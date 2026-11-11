package todos

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// peerCore is a window holding a crew scope, with other hosts' agents: what A lists beside its own,
// and every task it sends one of them.
type peerCore struct {
	*agentCore
	scope   string
	peers   []feature.AgentEntry
	sent    []peerSend
	sendErr error
}

type peerSend struct{ codename, scope, subject, body, key string }

func (c *peerCore) windowScope() string { return c.scope }
func (c *peerCore) peerAgents(then func([]feature.AgentEntry)) {
	if c.scope == "" {
		then(nil)
		return
	}
	then(c.peers)
}
func (c *peerCore) sendTask(codename, scope, subject, body, key string, then func(error)) {
	c.sent = append(c.sent, peerSend{codename, scope, subject, body, key})
	then(c.sendErr)
}

func peer(codename, scope, project string, reachable bool, why string) feature.AgentEntry {
	return feature.AgentEntry{Ref: feature.AgentRef{Codename: codename}, Name: codename,
		Peer: &feature.PeerAgent{Scope: scope, Project: project, Reachable: reachable, Why: why}}
}

func newPeerCore(scope string, peers ...feature.AgentEntry) (*peerCore, *[][]any) {
	ac, assigned := assigningStore(feature.AgentEntry{Ref: feature.AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true})
	return &peerCore{agentCore: ac, scope: scope, peers: peers}, assigned
}

// TestTodosAssignListsEveryConnectedAgent (item 17): A lists the window's own agents, then every
// other host's live crew agent, labelled codename@project; two scopes' agents of one codename are
// two rows, each its own key. A window holding no crew scope can't send to another host: it lists
// its own agents alone.
//
// Moved from tui/todos_assign_peers_test.go with its name and asserts unchanged.
func TestTodosAssignListsEveryConnectedAgent(t *testing.T) {
	c, _ := newPeerCore("autodoc:0123456789abcdef",
		peer("jarvis", "autovim:plugins", "plugins", true, ""),
		peer("zen", "autovim:plugins", "plugins", false, "no_endpoint"),
		peer("zen", "autovim:mcat", "mcat", true, ""))
	v := New(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	if !slices.Contains(c.Opened, "agentAssignPicker") || c.Sets["App.agentAssignStatus"] != "agents (4 of 4)" {
		t.Fatalf("A opened %v, listing %v; want the picker with the window's agent and 3 others", c.Opened, c.Sets["App.agentAssignStatus"])
	}
	var keys, labels []string
	for i, e := range v.assignPick.List.Items {
		r := v.assignPick.List.Row(i, e)
		keys, labels = append(keys, r["key"].(string)), append(labels, r["agent"].(string))
	}
	if !slices.Equal(keys, []string{"scribe", "jarvis@autovim:plugins", "zen@autovim:plugins", "zen@autovim:mcat"}) {
		t.Errorf("the rows' keys %v, want the window's agent, then each other agent by codename@scope", keys)
	}
	for _, want := range []string{"jarvis@plugins", "zen@plugins", "zen@mcat"} {
		if !slices.ContainsFunc(labels, func(l string) bool { return strings.Contains(l, want) }) {
			t.Errorf("no row labelled %s: %v", want, labels)
		}
	}

	none, _ := newPeerCore("", peer("jarvis", "autovim:plugins", "plugins", true, ""))
	w := New(none)
	w.list()
	onTask(t, w, "open")
	w.assign()
	if none.Sets["App.agentAssignStatus"] != "agents (1 of 1)" {
		t.Errorf("a window holding no crew scope lists %v, want its own agent alone", none.Sets["App.agentAssignStatus"])
	}
}

// TestTodosAssignHandsAnotherHostsAgentATaskAsTheUser (item 17): choosing another host's agent
// assigns the task to its bare codename (its host's own form) with the window's scope, so its own
// host's notifier leaves it to the window, and the window sends it, as the user, one Task: request
// to codename@scope: the task's file, id and the instruction, keyed by the assignment (a retry is
// the same send). Nothing is handed to the window's own agents, and the notice says it was sent.
//
// Moved from tui/todos_assign_peers_test.go with its name and asserts unchanged.
func TestTodosAssignHandsAnotherHostsAgentATaskAsTheUser(t *testing.T) {
	c, assigned := newPeerCore("autodoc:0123456789abcdef", peer("jarvis", "autovim:plugins", "plugins", true, ""))
	v := New(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	v.assignPick.Accept(1, "")
	if c.Sets["App.todoAssignTitle"] != "assign to jarvis@plugins" {
		t.Errorf("the Instructions: line's title %q", c.Sets["App.todoAssignTitle"])
	}
	v.assignSet("Go.")
	if len(*assigned) != 1 {
		t.Fatalf("todos.assign called %d times", len(*assigned))
	}
	if p := (*assigned)[0]; len(p) != 6 || p[3] != "jarvis" || p[4] != "Go." || p[5] != "autodoc:0123456789abcdef" {
		t.Errorf("todos.assign params %v, want the bare codename, the instruction and the window's scope", p)
	}
	if len(c.sent) != 1 {
		t.Fatalf("sent %d tasks, want one: %+v", len(c.sent), c.sent)
	}
	s := c.sent[0]
	if s.codename != "jarvis" || s.scope != "autovim:plugins" || s.subject != "Task: Ship" || s.key != "assign:t1:r2" ||
		!strings.Contains(s.body, "File: ") || !strings.Contains(s.body, filepath.Join("in-progress", "t1.md")) || !strings.Contains(s.body, "ID: t1") || !strings.Contains(s.body, "Instruction: Go.") {
		t.Errorf("the task sent %+v, want jarvis in autovim:plugins, Task: Ship, its file, id and instruction, keyed assign:t1:r2", s)
	}
	if len(c.handed) != 0 {
		t.Errorf("handed to the window's own agents %d times", len(c.handed))
	}
	if !slices.ContainsFunc(c.Notes, func(n string) bool { return strings.Contains(n, "jarvis@plugins") && strings.Contains(n, "sent") }) {
		t.Errorf("the notices %v, want one saying it was sent to jarvis@plugins", c.Notes)
	}
}

// TestTodosAssignQueuesForAnAgentNoWakeReaches (item 17): another host's agent no wake reaches now
// (no_endpoint) is still choosable; its task is sent, queued, and the notice says why it isn't woken.
// A send refused leaves the task assigned and says why.
//
// Moved from tui/todos_assign_peers_test.go with its name and asserts unchanged.
func TestTodosAssignQueuesForAnAgentNoWakeReaches(t *testing.T) {
	c, _ := newPeerCore("autodoc:0123456789abcdef", peer("lector", "autovim:plugins", "plugins", false, "no_endpoint"))
	v := New(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	v.assignPick.Accept(1, "")
	v.assignSet("")
	if len(c.sent) != 1 || !strings.Contains(c.sent[0].body, "Instruction: "+AssignDefault) {
		t.Fatalf("sent %+v, want the task, with the default instruction", c.sent)
	}
	if !slices.ContainsFunc(c.Notes, func(n string) bool {
		return strings.Contains(n, "queued") && strings.Contains(n, "lector@plugins") && strings.Contains(n, "no_endpoint")
	}) {
		t.Errorf("the notices %v, want queued: lector@plugins can't be woken now (no_endpoint)", c.Notes)
	}

	r, _ := newPeerCore("autodoc:0123456789abcdef", peer("jarvis", "autovim:plugins", "plugins", true, ""))
	r.sendErr = errors.New("mail.no_recipient")
	w := New(r)
	w.list()
	onTask(t, w, "open")
	w.assign()
	w.assignPick.Accept(1, "")
	w.assignSet("Go.")
	if !slices.ContainsFunc(r.Notes, func(n string) bool {
		return strings.Contains(n, "not sent") && strings.Contains(n, "mail.no_recipient")
	}) {
		t.Errorf("the notices %v, want assigned but not sent, with why", r.Notes)
	}
}

// slowPeerCore answers peerAgents only when answer runs, as a crew.list in flight does.
type slowPeerCore struct {
	*peerCore
	pending func()
}

func (c *slowPeerCore) peerAgents(then func([]feature.AgentEntry)) {
	c.pending = func() { c.peerCore.peerAgents(then) }
}

// TestTodosAssignAnsweredAfterAWorkspaceSwitchOpensNothing (item 17): A asks the crew for the other
// hosts' agents before it opens its picker; a workspace switched while that is in flight makes the
// answer stale: no picker opens, nothing is assigned, and no Task: request is sent.
//
// Moved from tui/todos_assign_peers_test.go with its name and asserts unchanged.
func TestTodosAssignAnsweredAfterAWorkspaceSwitchOpensNothing(t *testing.T) {
	pc, assigned := newPeerCore("autodoc:0123456789abcdef", peer("jarvis", "autovim:plugins", "plugins", true, ""))
	c := &slowPeerCore{peerCore: pc}
	v := New(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	c.switchWorkspace()
	if c.pending == nil {
		t.Fatal("A never asked the crew for the other hosts' agents")
	}
	c.pending()
	if slices.Contains(c.Opened, "agentAssignPicker") {
		t.Errorf("A opened its picker for a workspace no longer in use: %v", c.Opened)
	}
	v.assignPick.Accept(1, "")
	v.assignSet("Go.")
	if len(*assigned) != 0 || len(c.sent) != 0 {
		t.Errorf("after the switch: %d assigns, %d sends; want none", len(*assigned), len(c.sent))
	}
}

// TestTodosAssignPreviewsAnotherHostsAgent (item 17): another host's agent's preview is titled by
// codename@project and names its scope and whether a wake reaches it.
//
// Moved from tui/todos_assign_peers_test.go with its name and asserts unchanged.
func TestTodosAssignPreviewsAnotherHostsAgent(t *testing.T) {
	c, _ := newPeerCore("autodoc:0123456789abcdef", peer("zen", "autovim:mcat", "mcat", false, "no_endpoint"))
	v := New(c)
	e := c.peers[0]
	pn := v.assignPick.Preview(e)(context.Background()).Value
	if pn.Title != "zen@mcat" || !strings.Contains(pn.Text, "autovim:mcat") || !strings.Contains(pn.Text, runState(c, e)) {
		t.Errorf("the preview %q / %q: want zen@mcat, its scope and its state", pn.Title, pn.Text)
	}
}

// TestTodosAssignSendsNoPeerTaskFromAWindowWithoutACrew: a hand-over to another host's agent on a
// window that can't reach the crew sends nothing and says nothing.
//
// Moved from tui/todos_assign_peers_test.go with its name and asserts unchanged.
func TestTodosAssignSendsNoPeerTaskFromAWindowWithoutACrew(t *testing.T) {
	ac, _ := assigningStore(feature.AgentEntry{Ref: feature.AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true})
	v := New(ac)
	v.list()
	s, _ := v.cursor()
	before := len(ac.Notes)
	v.sendToPeer(&todoAssign{store: s, agent: peer("jarvis", "autovim:plugins", "plugins", true, "")}, s, map[string]any{"id": "t1"}, "Go.")
	if len(ac.Notes) != before {
		t.Errorf("a window without a crew noticed %v", ac.Notes[before:])
	}
}
