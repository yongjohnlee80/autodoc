package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

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
	pastes  []string
	sendErr error
	removal *modelRemoval
	addMenu func() []widget.MenuItemModel
	tabMenu func(widget.TerminalTabID) []widget.MenuItemModel
}

func (p *modelPanel) SameOwner(other TerminalPanel) bool {
	peer, ok := other.(*modelPanel)
	return ok && p == peer
}
func (p *modelPanel) Shutdown() TerminalCompletion {
	for i := range p.tabs {
		p.tabs[i].Running = false
	}
	if p.removal == nil {
		p.removal = &modelRemoval{done: make(chan struct{})}
		close(p.removal.done)
	}
	return p.removal
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
func (p *modelPanel) SendPaste(id widget.TerminalTabID, text string) error {
	if p.sendErr != nil {
		return p.sendErr
	}
	p.pastes = append(p.pastes, string(id)+":"+text)
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
func (p *modelPanel) Menus(add func() []widget.MenuItemModel, tab func(widget.TerminalTabID) []widget.MenuItemModel) {
	p.addMenu, p.tabMenu = add, tab
}

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
func (c *identityHost) After(d time.Duration, fn func()) {
	c.delays = append(c.delays, d)
	c.afters = append(c.afters, fn)
}

// fireAfters runs the waits After scheduled, as their timers would, then what they posted.
func (c *identityHost) fireAfters() {
	for len(c.afters) > 0 {
		afters := c.afters
		c.afters = nil
		for _, f := range afters {
			f()
		}
		c.drainPosts()
	}
}
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
	tab := string(instance.tab)
	if done != 0 || !slices.Equal(c.panel.pastes, []string{tab + ":Task: original\nFile: /tasks/one.md\nID: one\nInstruction: inspect"}) || len(c.panel.sends) != 0 {
		t.Fatalf("the task goes as one paste, its Enter not yet: done=%d pastes=%q sends=%q", done, c.panel.pastes, c.panel.sends)
	}
	if !slices.Equal(c.delays, []time.Duration{agentSubmitDelay}) {
		t.Fatalf("the Enter waits agentSubmitDelay: %v", c.delays)
	}
	c.fireAfters()
	if done != 1 || !slices.Equal(c.panel.sends, []string{tab + ":\r"}) {
		t.Fatalf("after the pause one Enter, a carriage return, and done: done=%d sends=%q", done, c.panel.sends)
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
			c.fireAfters()
			if done != 1 {
				t.Fatalf("completion count %d", done)
			}
			if ending == "yes" {
				if outcome != nil || c.panel == nil || len(c.panel.pastes) != 1 || len(c.panel.sends) != 1 {
					t.Fatal("consented target was not started and delivered")
				}
			} else {
				if outcome == nil || len(a.instances) != 0 || (c.panel != nil && len(c.panel.sends)+len(c.panel.pastes) > 0) {
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
			c.fireAfters()
			if outcome == nil || calls != 1 || len(c.panel.sends)+len(c.panel.pastes) != 0 {
				t.Fatal("deferred failure sent or missed completion")
			}
		})
	}
}

// TestHandTaskEnterIsCheckedAgainAfterThePause: the Enter that follows the task's paste is typed
// only into the same running agent, on the same parent, with the delivery still open. An agent
// that exited, a rebuilt parent, a shutdown or a failed write in the pause types no Enter, and done
// hears the reason once.
func TestHandTaskEnterIsCheckedAgainAfterThePause(t *testing.T) {
	for _, during := range []string{"exited", "rebuilt", "shutdown", "write"} {
		t.Run(during, func(t *testing.T) {
			a, c := modelAgents(t)
			p, _ := a.profile("one")
			instance, err := a.startInstance(p)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			var outcome error
			if err := a.HandTask(AgentRef{Codename: "one"}, TaskRef{Title: "task", Path: "/task.md"}, "work", func(err error) { calls++; outcome = err }); err != nil {
				t.Fatal(err)
			}
			c.drainPosts()
			if len(c.panel.pastes) != 1 || calls != 0 {
				t.Fatalf("the task was not pasted before the pause: pastes=%q calls=%d", c.panel.pastes, calls)
			}
			first := c.panel
			switch during {
			case "exited":
				c.panel.exit(instance.tab, 0)
			case "rebuilt":
				c.panel = &modelPanel{owner: c, next: 100}
			case "shutdown":
				a.Stopping()
			case "write":
				c.panel.sendErr = errors.New("queue full")
			}
			c.fireAfters()
			if len(first.sends) != 0 || len(c.panel.sends) != 0 {
				t.Fatalf("an Enter was typed after %s: %q %q", during, first.sends, c.panel.sends)
			}
			want := map[string]error{"exited": ErrAgentStale, "rebuilt": ErrAgentStale, "shutdown": ErrAgentShuttingDown}[during]
			if calls != 1 || outcome == nil || (want != nil && !errors.Is(outcome, want)) {
				t.Fatalf("done after %s: calls=%d outcome=%v", during, calls, outcome)
			}
		})
	}
}

// TestSendTextAddsNoEnter: Lua's SendText stays one paste with no Enter after it.
func TestSendTextAddsNoEnter(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	if _, err := a.startInstance(p); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := a.SendText(AgentRef{Codename: "one"}, "draft", func(err error) {
		if err != nil {
			t.Error(err)
		}
		calls++
	}); err != nil {
		t.Fatal(err)
	}
	c.drainPosts()
	c.fireAfters()
	if calls != 1 || len(c.panel.pastes) != 1 || len(c.panel.sends) != 0 || len(c.delays) != 0 {
		t.Fatalf("SendText: calls=%d pastes=%q sends=%q delays=%v", calls, c.panel.pastes, c.panel.sends, c.delays)
	}
}

func TestStoppedAgentDoesNotRelabelItsOldWorkspaceConfiguration(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	oldConfig := c.panel.configs[instance.tab]
	c.panel.exit(instance.tab, 3)
	c.ws = wsInfo{name: "other", root: t.TempDir()}
	c.epoch++
	done := 0
	var outcome error
	if err := a.HandTask(AgentRef{Codename: "one"}, TaskRef{Title: "other task", Path: "/other/task.md"}, "work", func(err error) { done++; outcome = err }); err != nil {
		t.Fatal(err)
	}
	c.questions[len(c.questions)-1].OnYes()
	if done != 1 || !errors.Is(outcome, ErrAgentStale) || instance.running || instance.workspace.name != "kb" || len(c.panel.sends)+len(c.panel.pastes) != 0 {
		t.Fatal("workspace B reused or relabelled workspace A process")
	}
	if c.panel.configs[instance.tab].Dir != oldConfig.Dir || c.panel.configs[instance.tab].Args[len(oldConfig.Args)-1] != oldConfig.Args[len(oldConfig.Args)-1] {
		t.Fatal("refused cross-workspace start mutated old configuration")
	}
}

func TestParentRebuildRetiresOldRefsAndCreatesCurrentOwnerInstances(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	oldRef := AgentRef{Codename: "one", Instance: instance.token}
	oldOwner := c.panel
	done := 0
	var outcome error
	if err := a.HandTask(oldRef, TaskRef{Title: "queued", Path: "/task.md"}, "work", func(err error) { done++; outcome = err }); err != nil {
		t.Fatal(err)
	}
	c.panel = &modelPanel{owner: c, next: 100}
	c.drainPosts()
	if done != 1 || !errors.Is(outcome, ErrAgentStale) || len(oldOwner.sends)+len(oldOwner.pastes) != 0 || len(c.panel.sends)+len(c.panel.pastes) != 0 {
		t.Fatal("queued old-parent request sent or lost completion")
	}
	callbacks := 0
	if err := a.HandTask(oldRef, TaskRef{Title: "stale", Path: "/task.md"}, "work", func(error) { callbacks++ }); !errors.Is(err, ErrAgentStale) || callbacks != 0 {
		t.Fatal("old ref survived parent retirement")
	}
	fresh, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == instance || fresh.token == oldRef.Instance || !fresh.owner.SameOwner(c.panel) || fresh.tab == instance.tab {
		t.Fatal("new instance still belongs to rebuilt old parent")
	}
	if err := a.selectInstance(fresh); err != nil {
		t.Fatal(err)
	}
	if err := a.removeInstance("one"); err != nil {
		t.Fatal(err)
	}
	if len(a.instances) != 0 {
		t.Fatal("current-owner removal used old-parent id")
	}
}

// TestUseAfterAParentRebuildNeverSelectsTheOldTab: the profiles dialog's Use, pressed after the
// terminal panel was rebuilt, retires the old parent's instance rather than selecting its tab on
// the new panel; once retired, Use starts a fresh instance on the current owner.
func TestUseAfterAParentRebuildNeverSelectsTheOldTab(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	old, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	c.panel = &modelPanel{owner: c, next: 100} // the parent rebuilt (a reload)
	a.useAgent(0)
	if c.panel.active == old.tab {
		t.Fatal("Use selected the old parent's tab on the new panel")
	}
	c.drainPosts() // the old owner's retirement completes
	a.useAgent(0)
	fresh := a.instances["one"]
	if fresh == nil || fresh == old || !fresh.running || !fresh.owner.SameOwner(c.panel) || c.panel.active != fresh.tab {
		t.Fatalf("after retirement Use did not start a current-owner instance: %+v", fresh)
	}
}
