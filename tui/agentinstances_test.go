package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

type modelRemoval struct {
	done chan struct{}
	err  error
}

func (r *modelRemoval) Done() <-chan struct{}      { return r.done }
func (r *modelRemoval) Wait(context.Context) error { <-r.done; return r.err }
func (*modelRemoval) Generation() int              { return 1 }

type modelPanel struct {
	owner   *identityHost
	configs map[widget.TerminalTabID]TerminalConfig
	tabs    []widget.TerminalTab
	active  widget.TerminalTabID
	next    int
	sends   []string
	sendErr error
	removal *modelRemoval
}

func (p *modelPanel) Add(label string, config TerminalConfig) (widget.TerminalTabID, error) {
	p.next++
	id := widget.TerminalTabID(fmt.Sprintf("tab-%d", p.next))
	if p.configs == nil {
		p.configs = map[widget.TerminalTabID]TerminalConfig{}
	}
	p.configs[id] = config
	p.tabs = append(p.tabs, widget.TerminalTab{ID: id, Label: label})
	if p.owner != nil && len(config.Args) > 0 {
		p.owner.configuredCommand = config.Args[len(config.Args)-1]
	}
	return id, nil
}
func (p *modelPanel) Select(id widget.TerminalTabID) error { p.active = id; return nil }
func (p *modelPanel) Start(id widget.TerminalTabID) error {
	for i := range p.tabs {
		if p.tabs[i].ID == id {
			p.tabs[i].Running = true
			p.tabs[i].Generation++
			return nil
		}
	}
	return ErrAgentUnavailable
}
func (p *modelPanel) SendText(id widget.TerminalTabID, text string) error {
	if p.sendErr != nil {
		return p.sendErr
	}
	p.sends = append(p.sends, string(id)+":"+text)
	return nil
}
func (p *modelPanel) Remove(id widget.TerminalTabID) (TerminalRemoval, error) {
	if p.owner != nil && p.owner.stopErr != nil {
		return nil, p.owner.stopErr
	}
	if p.removal == nil {
		p.removal = &modelRemoval{done: make(chan struct{})}
		close(p.removal.done)
	}
	return p.removal, nil
}
func (*modelPanel) ForceRemove(widget.TerminalTabID) error     { return nil }
func (p *modelPanel) Tabs() []widget.TerminalTab               { return append([]widget.TerminalTab(nil), p.tabs...) }
func (p *modelPanel) Active() widget.TerminalTabID             { return p.active }
func (*modelPanel) Actions(func(), func(widget.TerminalTabID)) {}

func (p *modelPanel) exit(id widget.TerminalTabID, code int) {
	for i := range p.tabs {
		if p.tabs[i].ID == id {
			p.tabs[i].Running = false
		}
	}
	if config, found := p.configs[id]; found && config.Exited != nil {
		config.Exited(code)
	}
}

func (c *identityHost) Terminals(string) (TerminalPanel, error) {
	if c.panel == nil {
		c.panel = &modelPanel{owner: c}
	}
	return c.panel, nil
}
func (*identityHost) OpenDrawer(string)  {}
func (*identityHost) CloseDrawer(string) {}
func (c *identityHost) Post(fn func())   { c.posts = append(c.posts, fn) }
func (c *identityHost) Ticket(_ *uint64, _ TicketScope) Ticket {
	epoch := c.epoch
	return Ticket{current: func() bool { return c.epoch == epoch }}
}
func (c *identityHost) Ask(q Question) Asked { c.questions = append(c.questions, q); return Asked{} }
func (c *identityHost) drainPosts() {
	for len(c.posts) > 0 {
		posts := c.posts
		c.posts = nil
		for _, f := range posts {
			f()
		}
	}
}

func modelAgents(t *testing.T) (*agents, *identityHost) {
	t.Helper()
	c := newIdentityHost(`[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`)
	c.ws = wsInfo{name: "kb", root: t.TempDir()}
	a := newAgents(c, t.TempDir(), "")
	return a, c
}

func retainModelInstance(a *agents, c *identityHost, code, label string) {
	panel, _ := c.Terminals("agentView")
	id, _ := panel.Add(label, TerminalConfig{})
	_ = panel.Start(id)
	p, exists := a.profile(code)
	if !exists {
		p = agentProfile{Name: label, Codename: code, Command: "orphan"}
	}
	a.instances[code] = &agentInstance{profile: p, tab: id, token: "retained-" + code, running: true, owner: panel, generation: 1, workspace: c.ws}
	a.instanceOrder = append(a.instanceOrder, code)
	a.selectedAgent, a.agentRunning, a.agentRunningLabel = code, code, label
}

func TestAgentInstancesRunTogetherAndRefsDoNotFollowRestarts(t *testing.T) {
	a, c := modelAgents(t)
	one, _ := a.profile("one")
	two, _ := a.profile("two")
	first, err := a.startInstance(one)
	if err != nil {
		t.Fatal(err)
	}
	old := AgentRef{Codename: "one", Instance: first.token}
	if _, err := a.startInstance(two); err != nil {
		t.Fatal(err)
	}
	if !first.running || len(a.AgentEntries()) != 2 || !a.instances["two"].running {
		t.Fatal("starting another agent stopped the first")
	}
	c.panel.exit(first.tab, 0)
	if _, err := a.startInstance(one); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	err = a.HandTask(old, TaskRef{Title: "task", Path: "/tasks/task.md"}, "work", func(error) { callbacks++ })
	if !errors.Is(err, ErrAgentStale) || callbacks != 0 || len(c.posts) != 0 {
		t.Fatal("stale ref was accepted or completed")
	}
}

func TestHandTaskAcceptedDeliverySnapshotsAndCompletesOnce(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	done := 0
	task := TaskRef{Title: "original", Path: "/tasks/one.md", ID: "one"}
	if err := a.HandTask(AgentRef{Codename: "one"}, task, "inspect", func(err error) {
		if err != nil {
			t.Error(err)
		}
		done++
	}); err != nil {
		t.Fatal(err)
	}
	task.Title = "changed"
	if done != 0 {
		t.Fatal("running delivery completed outside its later loop turn")
	}
	c.drainPosts()
	if done != 1 || len(c.panel.sends) != 1 || c.panel.sends[0] != string(instance.tab)+":Task: original\nFile: /tasks/one.md\nID: one\nInstruction: inspect\n" {
		t.Fatalf("delivery done=%d sends=%q", done, c.panel.sends)
	}
	c.drainPosts()
	a.Stopping()
	if done != 1 {
		t.Fatal("completed delivery completed again at shutdown")
	}
}

func TestHandTaskStoppedConsentRevalidatesAndNeverSendsAfterCancellation(t *testing.T) {
	for _, ending := range []string{"no", "workspace", "profile", "shutdown", "yes"} {
		t.Run(ending, func(t *testing.T) {
			a, c := modelAgents(t)
			done := 0
			var outcome error
			if err := a.HandTask(AgentRef{Codename: "one"}, TaskRef{Title: "task", Path: "/tasks/task.md"}, "work", func(err error) { done++; outcome = err }); err != nil {
				t.Fatal(err)
			}
			if len(c.questions) != 1 || len(a.instances) != 0 {
				t.Fatal("stopped target started before consent")
			}
			q := c.questions[0]
			switch ending {
			case "no":
				q.OnCancel()
			case "workspace":
				c.epoch++
				q.OnYes()
			case "profile":
				c.p.values[prefAgentProfiles] = `[]`
				q.OnYes()
			case "shutdown":
				a.Stopping()
				q.OnCancel()
			case "yes":
				q.OnYes()
			}
			c.drainPosts()
			if done != 1 {
				t.Fatalf("completion count %d", done)
			}
			if ending == "yes" {
				if outcome != nil || c.panel == nil || len(c.panel.sends) != 1 {
					t.Fatal("consented target was not started and delivered")
				}
			} else {
				if outcome == nil || len(a.instances) != 0 || (c.panel != nil && len(c.panel.sends) > 0) {
					t.Fatal("cancelled/stale request started or sent")
				}
			}
		})
	}
}

func TestHandTaskQueuedTargetReplacementAndSendFailureAreCompletionErrors(t *testing.T) {
	for _, failure := range []string{"replacement", "write"} {
		t.Run(failure, func(t *testing.T) {
			a, c := modelAgents(t)
			p, _ := a.profile("one")
			instance, err := a.startInstance(p)
			if err != nil {
				t.Fatal(err)
			}
			var outcome error
			calls := 0
			if err := a.HandTask(AgentRef{Codename: "one", Instance: instance.token}, TaskRef{Title: "task", Path: "/task.md"}, "work", func(err error) { outcome = err; calls++ }); err != nil {
				t.Fatal(err)
			}
			if failure == "replacement" {
				instance.token = "replacement"
			} else {
				c.panel.sendErr = errors.New("queue full")
			}
			c.drainPosts()
			if outcome == nil || calls != 1 || len(c.panel.sends) != 0 {
				t.Fatal("deferred failure sent or missed completion")
			}
		})
	}
}
