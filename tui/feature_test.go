package tui

import (
	"context"
	"strings"
	"testing"
)

// fake is a feature that records what it hears.
type fake struct {
	name  string
	state map[string]any
	cmds  map[string]Command
	heard []string
	kinds []string
}

func (f *fake) Name() string                  { return f.name }
func (f *fake) State() map[string]any         { return f.state }
func (f *fake) Commands() map[string]Command  { return f.cmds }
func (f *fake) Connected()                    { f.heard = append(f.heard, "connected") }
func (f *fake) Entered(ws string)             { f.heard = append(f.heard, "entered "+ws) }
func (f *fake) Kinds() []string               { return f.kinds }
func (f *fake) Event(kind, ws, detail string) { f.heard = append(f.heard, kind+" "+ws+" "+detail) }

var noop = none(func() {})

func TestTheRegistryMergesFeatures(t *testing.T) {
	a := &fake{name: "a", state: map[string]any{"App.a": 1}, cmds: map[string]Command{"App.doA": noop}}
	b := &fake{name: "b", state: map[string]any{"App.b": "x"}, cmds: map[string]Command{"App.doB": noop}, kinds: []string{"b.changed"}}
	r, err := newRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.state["App.a"] != 1 || r.state["App.b"] != "x" || r.handlers["App.doA"] == nil || r.handlers["App.doB"] == nil {
		t.Errorf("merged %v %v", r.state, r.handlers)
	}
	r.connected()
	r.entered("kb")
	if !r.event("b.changed", "kb", "t1") || r.event("other", "", "") {
		t.Error("an event went to the wrong feature")
	}
	if got := strings.Join(b.heard, "|"); got != "connected|entered kb|b.changed kb t1" {
		t.Errorf("b heard %q", got)
	}
	var none *registry
	none.connected()
	none.entered("kb")
	if none.event("b.changed", "", "") {
		t.Error("an absent registry heard an event")
	}
}

func TestTheRegistryRefusesANameTwoFeaturesClaim(t *testing.T) {
	for name, b := range map[string]*fake{
		"a source":                    {name: "b", state: map[string]any{"App.a": 2}},
		"a command":                   {name: "b", cmds: map[string]Command{"App.doA": noop}},
		"a source named as a command": {name: "b", cmds: map[string]Command{"App.a": noop}},
	} {
		a := &fake{name: "a", state: map[string]any{"App.a": 1}, cmds: map[string]Command{"App.doA": noop}}
		if _, err := newRegistry(a, b); err == nil || !strings.Contains(err.Error(), "both a's and b's") {
			t.Errorf("%s claimed twice: %v", name, err)
		}
	}
}

// TestTheLegacyFeatureIsTheWholeHost: the host's own state and commands are one feature, so the
// program is what it was.
func TestTheLegacyFeatureIsTheWholeHost(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	n := onLoop(r, func() [2]int { return [2]int{len(r.h.reg.state), len(r.h.reg.handlers)} })
	want := onLoop(r, func() [2]int {
		var w [2]int
		for _, f := range r.h.reg.features {
			w[0] += len(f.State())
			w[1] += len(f.Commands())
		}
		return w
	})
	if n != want {
		t.Errorf("the registry has %v sources and commands, its features %v", n, want)
	}
	for k := range onLoop(r, r.h.state) {
		if _, ok := onLoop(r, func() map[string]any { return r.h.reg.state })[k]; !ok {
			t.Errorf("the host's %s is not in the registry", k)
		}
	}
}

// TestAFileTicketIsStaleWhenAnotherFileOpens: a file-scoped answer is stale once another file
// opens; a workspace-scoped one is not. With no sequence, nothing newer makes either stale.
func TestAFileTicketIsStaleWhenAnotherFileOpens(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	got := onLoop(r, func() [4]bool {
		file, ws := r.h.ticket(nil, TicketFile), r.h.ticket(nil, TicketWorkspace)
		r.h.ticket(nil, TicketFile) // no sequence: a later ticket supersedes nothing
		before := [2]bool{file.Current(), ws.Current()}
		r.h.file.gen++ // another file opened
		defer func() { r.h.file.gen-- }()
		return [4]bool{before[0], before[1], file.Current(), ws.Current()}
	})
	if got != [4]bool{true, true, false, true} {
		t.Errorf("current: file %v, workspace %v; after another file opened: file %v, workspace %v", got[0], got[1], got[2], got[3])
	}
}

// TestATicketIsStaleAfterANewerOneOrAWorkspaceSwitch: the latest answer wins, and an answer asked
// under another workspace never applies.
func TestATicketIsStaleAfterANewerOneOrAWorkspaceSwitch(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	got := onLoop(r, func() [4]bool {
		c := core{r.h}
		var seq uint64
		first := c.Ticket(&seq, TicketWorkspace)
		second := c.Ticket(&seq, TicketWorkspace)
		session := c.Ticket(new(uint64), TicketSession)
		ws := c.Ticket(new(uint64), TicketWorkspace)
		before := [2]bool{first.Current(), second.Current()}
		r.h.epoch++ // a workspace switch
		defer func() { r.h.epoch-- }()
		return [4]bool{before[0], before[1], session.Current(), ws.Current()}
	})
	if got != [4]bool{false, true, true, false} {
		t.Errorf("current: first %v, second %v, session-scoped after a switch %v, workspace-scoped after a switch %v", got[0], got[1], got[2], got[3])
	}
}

// TestCoreReachesTheHost: what a feature does through Core lands on the host.
func TestCoreReachesTheHost(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	c := core{r.h}
	if ws := onLoop(r, c.Workspace); ws != "kb" {
		t.Errorf("workspace %q", ws)
	}
	onLoop(r, func() bool { c.Notify("from a feature"); return true })
	r.waitNotice(t, "from a feature")
	out := make(chan any, 1)
	onLoop(r, func() bool {
		c.Go(func(ctx context.Context) any {
			v, err := c.Call(ctx, "workspace.list")
			if err != nil {
				return err
			}
			return v
		}, func(v any) { out <- v })
		return true
	})
	if v := <-out; v == nil {
		t.Error("no answer from the daemon through Core")
	} else if err, ok := v.(error); ok {
		t.Error(err)
	}
}
