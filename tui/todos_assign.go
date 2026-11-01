package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
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
	p := newAgentPick(t.c, "agentAssignPicker", "agentAssign", t.assignTo)
	p.scope = TicketWorkspace
	return p
}

// newAgentPick is a picker of AutoDoc's agents, by name and codename, each previewed with what it
// runs: A's (todos_assign.go) and Audit…'s (candidate.go). Its sources are App.<name>Rows, Field,
// Status and Preview*.
func newAgentPick(c Core, dialog, name string, choose func(AgentEntry)) *pick[AgentEntry] {
	return &pick[AgentEntry]{c: c, dialog: dialog, name: name,
		list:   newRows(tuidecl.NewListModel("key", "agent", "state"), func(_ int, e AgentEntry) rowOf { return agentRowOf(c, e) }),
		field:  "App." + name + "Field",
		status: "App." + name + "Status", noun: "agents",
		match: func(e AgentEntry) string { return e.Name + " " + e.Ref.Codename },
		preview: func(e AgentEntry) func(context.Context) answerOf[pane] {
			pn := agentPane(c, e)
			return func(context.Context) answerOf[pane] { return answerOf[pane]{v: pn} }
		},
		choose: choose}
}

// agentRowOf is an agent's row, in c's language.
func agentRowOf(c Core, e AgentEntry) rowOf {
	if e.Peer != nil { // another host's: keyed by its address, labelled by its project
		return rowOf{"key": e.Ref.Codename + "@" + e.Peer.Scope, "agent": peerLabel(e), "state": runState(c, e)}
	}
	return rowOf{"key": e.Ref.Codename, "agent": e.Name + "  (" + e.Ref.Codename + ")", "state": runState(c, e)}
}

// peerLabel is another host's agent as the user knows it: codename@project.
func peerLabel(e AgentEntry) string { return e.Ref.Codename + "@" + e.Peer.Project }

func runState(c Core, e AgentEntry) string {
	if e.Peer != nil {
		if e.Peer.Reachable {
			return c.Tr("autodoc.text.todos_assign.peer_can_be_woken")
		}
		return c.Trf("autodoc.text.todos_assign.peer_cant_be_woken_now", e.Peer.Why)
	}
	if e.Running {
		return c.Tr("autodoc.text.todos_assign.running")
	}
	return c.Tr("autodoc.text.todos_assign.stopped")
}

// agentPane is an agent's preview: who it is, what it runs, and what A does to a stopped one, in c's
// language.
func agentPane(c Core, e AgentEntry) pane {
	var b strings.Builder
	if e.Peer != nil {
		fmt.Fprintf(&b, "## %s\n\n%s\n", peerLabel(e), c.Trf("autodoc.text.todos_assign.peer_pane", e.Peer.Scope, runState(c, e)))
		return pane{title: peerLabel(e), text: b.String()}
	}
	fmt.Fprintf(&b, "## %s\n\n%s\n", e.Name, c.Trf("autodoc.text.todos_assign.codename_assignee_command",
		e.Ref.Codename, assigneePrefix+e.Ref.Codename, e.Command, runState(c, e)))
	if !e.Running {
		b.WriteString("\n" + c.Tr("autodoc.text.todos_assign.not_running_you_ll_be_asked_to_start_it_and_the") + "\n")
	}
	if e.Orphan {
		b.WriteString("\n" + c.Tr("autodoc.text.todos_assign.its_profile_is_gone_it_takes_the_task_while_it_r") + "\n")
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
// agents in the picker. Off a task, or on a released store, it does nothing: the action row
// offers it only where it applies.
func (t *todos) assign() {
	s, x := t.cursor()
	if x == nil || t.readOnly(s) {
		return
	}
	own := assignable(t.c.AgentEntries())
	open := func(agents []AgentEntry) {
		if len(agents) == 0 {
			t.c.Notify(t.c.Tr("autodoc.notice.todos_assign.todos_no_autodoc_agent_to_assign_to_add_one_in_t"))
			return
		}
		t.assigning = &todoAssign{store: s, task: x, ws: t.c.Ticket(nil, TicketWorkspace)}
		t.assignPick.setAll(agents)
		t.assignPick.open()
	}
	p, ok := t.c.(crewPeers)
	if !ok {
		open(own)
		return
	}
	// every other host's live agent too (item 17), read as A opens
	p.peerAgents(func(peers []AgentEntry) { open(append(own, peers...)) })
}

// assignTo is the agent chosen: the Instructions: line is asked for, emptied.
func (t *todos) assignTo(e AgentEntry) {
	if t.assigning == nil {
		return
	}
	t.assigning.agent = e
	name := e.Name
	if e.Peer != nil {
		name = peerLabel(e)
	}
	t.c.Set("App.todoAssignTitle", t.c.Trf("autodoc.text.todos_assign.assign_to", name))
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
	if a.agent.Peer != nil {
		// another host's agent: its host's own form, the bare codename, so its panel shows it as its
		// agent's; the window's scope below makes every Neovim's notifier leave it to the window
		assignee = a.agent.Ref.Codename
	}
	s, x := a.store, a.task
	from := str(x, "status")
	params := []any{s.at, str(x, "id"), str(x, "rev"), assignee, instruction}
	if w, ok := t.c.(windowScoped); ok {
		if scope := w.windowScope(); scope != "" { // the window's own: its assignment feed leaves this one to handOver (crewtodos.go)
			params = append(params, scope)
		}
	}
	t.write(s, "todos.assign", params, func(v any) {
		if !a.ws.Current() { // the assignment stands; another workspace's agent never hears of it
			t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_but_not_delivered_the_workspac", assignee))
			return
		}
		task := asMap(v)
		if to := str(task, "status"); to != "" && to != from && s == t.cur { // an open task is in progress now (D1)
			t.followTask(task, to)
		}
		if a.agent.Peer != nil {
			t.sendToPeer(a, s, task, instruction)
			return
		}
		t.handOver(a, s, task, assignee, instruction)
	})
}

// sendToPeer sends another host's agent the task, as the assign wrote it, as the user's request:
// the user chose it here, in A (the one path that sends a Task: request to another scope; an
// assignment made anywhere else reaches only the window's own agents, forwarded: crewtodos.go). It
// is queued and woken as any mail, never typed into another host's terminal, and keyed by the
// assignment, so a retry is the same send.
func (t *todos) sendToPeer(a *todoAssign, s *todoStore, task map[string]any, instruction string) {
	p, ok := t.c.(crewPeers)
	if !ok {
		return
	}
	ref := TaskRef{Title: str(task, "title"), Path: filepath.Join(s.path, filepath.FromSlash(str(task, "path"))), ID: str(task, "id")}
	m := taskMessage(ref, instruction)
	e := a.agent
	p.sendTask(e.Ref.Codename, e.Peer.Scope, m.Subject, m.Body, "assign:"+str(task, "id")+":"+str(task, "rev"), func(err error) {
		switch {
		case err != nil:
			t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_peer_not_sent", peerLabel(e), err.Error()))
		case !e.Peer.Reachable:
			t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_peer_queued", peerLabel(e), e.Peer.Why))
		default:
			t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_peer_sent", peerLabel(e)))
		}
	})
}

// crewPeers is a Core whose window holds a crew scope and can hand a task to another host's agent:
// peerAgents answers, on the loop, the live crew agents of every other scope (crew.list), and
// sendTask sends codename@scope the task as the user's request (inbox.send from the window's
// scope, never rung: queued and woken as any mail).
type crewPeers interface {
	peerAgents(then func([]AgentEntry))
	sendTask(codename, scope, subject, body, key string, then func(error))
}

// peersOf are crew.list's rows (rows) A may hand a task to beside the window's own agents: every
// live agent of a scope other than own.
func peersOf(rows any, own string) []AgentEntry {
	var out []AgentEntry
	for _, r := range asList(rows) {
		m := asMap(r)
		code, scope, presence := str(m, "codename"), str(m, "scope"), str(m, "presence")
		if code == "" || scope == "" || scope == own || scope == mail.GlobalScope || // the user's and the daemon's own
			(presence != crew.PresenceLive && presence != crew.PresenceReconnecting) {
			continue
		}
		project := scope
		if name, ok := strings.CutPrefix(scope, "autovim:"); ok {
			project = name
		}
		reachable, _ := m["reachable"].(bool)
		out = append(out, AgentEntry{Ref: AgentRef{Codename: code}, Name: code,
			Peer: &PeerAgent{Scope: scope, Project: project, Reachable: reachable, Why: str(m, "why")}})
	}
	return out
}

// windowScoped is a Core that knows the window's crew scope ("" while it holds none): A assigns
// with it, so the window's assignment feed knows the assignment for one A hands over itself.
type windowScoped interface{ windowScope() string }

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

// handedOver says how the hand-over went. The assignment stands either way. Delivered, the task
// reached the agent through the window's agent messaging (agentmessaging.go): rung, or typed and
// sent (docs/decisions/todos.md#a-message-to-an-agent-is-sent-never-left-for-enter).
func (t *todos) handedOver(name, assignee string, err error) {
	if err == nil {
		t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_sent", name))
		return
	}
	if errors.Is(err, ErrAgentTypedNotSent) { // typed, left for the user's Enter (agentmessaging.go)
		t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_typed_into_its_input", name))
		return
	}
	if errors.Is(err, widget.ErrTerminalPasteUnbracketed) {
		t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_its_program_takes_no_bracketed_paste", name))
		return
	}
	var e *agentElsewhere
	if errors.As(err, &e) { // running, in another workspace: neither rung nor typed
		t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_but_not_delivered_it_runs_in", name, e.workspace, e.why.Error()))
		return
	}
	why := err.Error()
	if errors.Is(err, ErrAgentCancelled) {
		why = t.c.Tr("autodoc.notice.todos_assign.you_didn_t_start_it")
	}
	t.c.Notify(t.c.Trf("autodoc.notice.todos_assign.todos_assigned_to_but_not_delivered_start_the_ag", assignee, why))
}
