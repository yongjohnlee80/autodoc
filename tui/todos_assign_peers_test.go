package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// peerCore is a window holding a crew scope, with other hosts' agents: what A lists beside its own,
// and every task it sends one of them.
type peerCore struct {
	*agentCore
	scope   string
	peers   []AgentEntry
	sent    []peerSend
	sendErr error
}

type peerSend struct{ codename, scope, subject, body, key string }

func (c *peerCore) windowScope() string { return c.scope }
func (c *peerCore) peerAgents(then func([]AgentEntry)) {
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

func peer(codename, scope, project string, reachable bool, why string) AgentEntry {
	return AgentEntry{Ref: AgentRef{Codename: codename}, Name: codename,
		Peer: &PeerAgent{Scope: scope, Project: project, Reachable: reachable, Why: why}}
}

func newPeerCore(scope string, peers ...AgentEntry) (*peerCore, *[][]any) {
	ac, assigned := assigningStore(AgentEntry{Ref: AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true})
	return &peerCore{agentCore: ac, scope: scope, peers: peers}, assigned
}

// TestTodosAssignListsEveryConnectedAgent (item 17): A lists the window's own agents, then every
// other host's live crew agent, labelled codename@project; two scopes' agents of one codename are
// two rows, each its own key. A window holding no crew scope can't send to another host: it lists
// its own agents alone.
func TestTodosAssignListsEveryConnectedAgent(t *testing.T) {
	c, _ := newPeerCore("autodoc:0123456789abcdef",
		peer("jarvis", "autovim:plugins", "plugins", true, ""),
		peer("zen", "autovim:plugins", "plugins", false, "no_endpoint"),
		peer("zen", "autovim:mcat", "mcat", true, ""))
	v := newTodos(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	if !slices.Contains(c.opened, "agentAssignPicker") || c.set["App.agentAssignStatus"] != "agents (4 of 4)" {
		t.Fatalf("A opened %v, listing %v; want the picker with the window's agent and 3 others", c.opened, c.set["App.agentAssignStatus"])
	}
	var keys, labels []string
	for i, e := range v.assignPick.list.items {
		r := v.assignPick.list.row(i, e)
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
	w := newTodos(none)
	w.list()
	onTask(t, w, "open")
	w.assign()
	if none.set["App.agentAssignStatus"] != "agents (1 of 1)" {
		t.Errorf("a window holding no crew scope lists %v, want its own agent alone", none.set["App.agentAssignStatus"])
	}
}

// TestTodosAssignHandsAnotherHostsAgentATaskAsTheUser (item 17): choosing another host's agent
// assigns the task to its bare codename (its host's own form) with the window's scope, so its own
// host's notifier leaves it to the window, and the window sends it, as the user, one Task: request
// to codename@scope: the task's file, id and the instruction, keyed by the assignment (a retry is
// the same send). Nothing is handed to the window's own agents, and the notice says it was sent.
func TestTodosAssignHandsAnotherHostsAgentATaskAsTheUser(t *testing.T) {
	c, assigned := newPeerCore("autodoc:0123456789abcdef", peer("jarvis", "autovim:plugins", "plugins", true, ""))
	v := newTodos(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	v.assignPick.accept(1, "")
	if c.set["App.todoAssignTitle"] != "assign to jarvis@plugins" {
		t.Errorf("the Instructions: line's title %q", c.set["App.todoAssignTitle"])
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
		!strings.Contains(s.body, "File: ") || !strings.Contains(s.body, "in-progress/t1.md") || !strings.Contains(s.body, "ID: t1") || !strings.Contains(s.body, "Instruction: Go.") {
		t.Errorf("the task sent %+v, want jarvis in autovim:plugins, Task: Ship, its file, id and instruction, keyed assign:t1:r2", s)
	}
	if len(c.handed) != 0 {
		t.Errorf("handed to the window's own agents %d times", len(c.handed))
	}
	if !slices.ContainsFunc(c.notes, func(n string) bool { return strings.Contains(n, "jarvis@plugins") && strings.Contains(n, "sent") }) {
		t.Errorf("the notices %v, want one saying it was sent to jarvis@plugins", c.notes)
	}
}

// TestTodosAssignQueuesForAnAgentNoWakeReaches (item 17): another host's agent no wake reaches now
// (no_endpoint) is still choosable; its task is sent, queued, and the notice says why it isn't woken.
// A send refused leaves the task assigned and says why.
func TestTodosAssignQueuesForAnAgentNoWakeReaches(t *testing.T) {
	c, _ := newPeerCore("autodoc:0123456789abcdef", peer("lector", "autovim:plugins", "plugins", false, "no_endpoint"))
	v := newTodos(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	v.assignPick.accept(1, "")
	v.assignSet("")
	if len(c.sent) != 1 || !strings.Contains(c.sent[0].body, "Instruction: "+assignDefault) {
		t.Fatalf("sent %+v, want the task, with the default instruction", c.sent)
	}
	if !slices.ContainsFunc(c.notes, func(n string) bool {
		return strings.Contains(n, "queued") && strings.Contains(n, "lector@plugins") && strings.Contains(n, "no_endpoint")
	}) {
		t.Errorf("the notices %v, want queued: lector@plugins can't be woken now (no_endpoint)", c.notes)
	}

	r, _ := newPeerCore("autodoc:0123456789abcdef", peer("jarvis", "autovim:plugins", "plugins", true, ""))
	r.sendErr = errors.New("mail.no_recipient")
	w := newTodos(r)
	w.list()
	onTask(t, w, "open")
	w.assign()
	w.assignPick.accept(1, "")
	w.assignSet("Go.")
	if !slices.ContainsFunc(r.notes, func(n string) bool {
		return strings.Contains(n, "not sent") && strings.Contains(n, "mail.no_recipient")
	}) {
		t.Errorf("the notices %v, want assigned but not sent, with why", r.notes)
	}
}

// TestPeersOfAreOtherScopesLiveAgents (item 17): of crew.list's rows, A lists another scope's live
// or reconnecting agents, labelled by project (an AutoVim scope's name; another scope as it is), and
// never the window's own scope (its agents are A's own rows), an ended profile, the user or the
// daemon's own profile.
func TestPeersOfAreOtherScopesLiveAgents(t *testing.T) {
	row := func(codename, scope, presence string, reachable bool, why string) any {
		return map[string]any{"codename": codename, "scope": scope, "presence": presence, "reachable": reachable, "why": why}
	}
	own := "autodoc:0123456789abcdef"
	got := peersOf([]any{
		row("scribe", own, "live", true, ""),
		row("jarvis", "autovim:plugins", "live", true, ""),
		row("lector", "autovim:plugins", "live", false, "no_endpoint"),
		row("vision", "autovim:plugins", "reconnecting", true, ""),
		row("ghost", "autovim:plugins", "ended", false, "ended"),
		row("user", "global", "live", true, ""),
		row("autodoc", "global", "live", true, ""),
		row("tifa", "autodoc:fedcba9876543210", "live", true, ""),
	}, own)
	var names []string
	for _, e := range got {
		names = append(names, e.Ref.Codename+"@"+e.Peer.Project+"|"+e.Peer.Scope)
	}
	want := []string{"jarvis@plugins|autovim:plugins", "lector@plugins|autovim:plugins", "vision@plugins|autovim:plugins",
		"tifa@autodoc:fedcba9876543210|autodoc:fedcba9876543210"}
	if !slices.Equal(names, want) {
		t.Errorf("peers %v, want %v", names, want)
	}
	for _, e := range got {
		if e.Ref.Codename == "lector" && (e.Peer.Reachable || e.Peer.Why != "no_endpoint") {
			t.Errorf("lector %+v, want unreachable, no_endpoint", e.Peer)
		}
	}
}

// slowPeerCore answers peerAgents only when answer runs, as a crew.list in flight does.
type slowPeerCore struct {
	*peerCore
	pending func()
}

func (c *slowPeerCore) peerAgents(then func([]AgentEntry)) {
	c.pending = func() { c.peerCore.peerAgents(then) }
}

// TestTodosAssignAnsweredAfterAWorkspaceSwitchOpensNothing (item 17): A asks the crew for the other
// hosts' agents before it opens its picker; a workspace switched while that is in flight makes the
// answer stale: no picker opens, nothing is assigned, and no Task: request is sent.
func TestTodosAssignAnsweredAfterAWorkspaceSwitchOpensNothing(t *testing.T) {
	pc, assigned := newPeerCore("autodoc:0123456789abcdef", peer("jarvis", "autovim:plugins", "plugins", true, ""))
	c := &slowPeerCore{peerCore: pc}
	v := newTodos(c)
	v.list()
	onTask(t, v, "open")
	v.assign()
	c.switchWorkspace()
	if c.pending == nil {
		t.Fatal("A never asked the crew for the other hosts' agents")
	}
	c.pending()
	if slices.Contains(c.opened, "agentAssignPicker") {
		t.Errorf("A opened its picker for a workspace no longer in use: %v", c.opened)
	}
	v.assignPick.accept(1, "")
	v.assignSet("Go.")
	if len(*assigned) != 0 || len(c.sent) != 0 {
		t.Errorf("after the switch: %d assigns, %d sends; want none", len(*assigned), len(c.sent))
	}
}
