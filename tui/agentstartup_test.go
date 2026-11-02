package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// A crew Claude agent is past its startup once Claude Code ran its SessionStart hook, which
// registers its endpoint; before that it may be showing its folder-trust question, whose default
// is "No, exit". Its cells: claudeOne is crewAgents with one Claude agent, cl, started.
const claudeOne = `[{"name":"Claude","command":"one","kind":"claude","codename":"cl"}]`

func claudeRig(t *testing.T) (*agents, *identityHost, *crewWindow, *agentInstance) {
	t.Helper()
	a, c, w := crewAgents(t, claudeOne)
	p, _ := a.profile("cl")
	instance, err := a.startInstance(p)
	if err != nil || !instance.running {
		t.Fatalf("start: %v", err)
	}
	a.promptsReported = func(string, bool) bool { return true }
	return a, c, w, instance
}

// started gives codename's live session an endpoint, as its SessionStart hook registers once
// Claude Code is past its startup.
func (d *crewDaemon) started(t *testing.T, codename string) {
	t.Helper()
	if err := d.mem.Write(context.Background(), func(tx crew.Tx) error {
		profiles, err := tx.ActiveProfiles()
		if err != nil {
			return err
		}
		for _, p := range profiles {
			if p.Codename != codename {
				continue
			}
			s, live, err := tx.LiveSession(p.ID)
			if err != nil || !live {
				return err
			}
			return tx.PutEndpoint(crew.Endpoint{Session: s.ID, Generation: 1, Kind: p.Kind})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// notStartedNotices counts the notices that say codename hasn't finished starting in folder.
func notStartedNotices(notes []string, name, folder string) int {
	n := 0
	for _, note := range notes {
		if strings.Contains(note, name) && strings.Contains(note, folder) && strings.Contains(note, "trust") {
			n++
		}
	}
	return n
}

// Nothing is typed into a crew Claude that hasn't started: no paste, no Enter, whatever is
// delivered (a fallback job here), since a paste into its folder-trust question may act on its keys
// and an Enter answers it. The delivery ends unsent, and the user is told once per session, naming
// the agent and its folder, however many deliveries come. Once it has started, a delivery is typed
// and sent as before (the mutants: the check skipped; the notice per delivery).
func TestNothingIsTypedIntoAClaudeThatHasntStarted(t *testing.T) {
	a, c, w, instance := claudeRig(t)
	var heard []error
	for range 2 {
		a.deliverFallback("cl", "hello cl", func(err error) { heard = append(heard, err) })
		settleMessaging(c, w)
	}
	if len(c.panel.pastes) != 0 || len(c.panel.keys) != 0 {
		t.Fatalf("pasted %q, pressed %q into a Claude that hasn't started: want nothing", c.panel.pastes, c.panel.keys)
	}
	if len(heard) != 2 || heard[0] == nil || heard[1] == nil {
		t.Fatalf("the deliveries heard %v: want each ended unsent", heard)
	}
	if n := notStartedNotices(c.notes, "Claude", instance.dir); n != 1 {
		t.Fatalf("notices %q: want one that Claude hasn't finished starting in %s, once over two deliveries", c.notes, instance.dir)
	}

	w.d.started(t, "cl")
	heard = nil
	a.deliverFallback("cl", "hello cl", func(err error) { heard = append(heard, err) })
	settleMessaging(c, w)
	if len(c.panel.pastes) != 1 || len(c.panel.keys) != 1 || len(heard) != 1 || heard[0] != nil {
		t.Fatalf("started: pasted %q, pressed %q, heard %v; want it typed and sent", c.panel.pastes, c.panel.keys, heard)
	}
}

// The user's own send to a Claude that hasn't started, when its doorbell can't ring, types nothing
// either: the base checks it as it checks a fallback job.
func TestTheUsersSendIsNotTypedIntoAClaudeThatHasntStarted(t *testing.T) {
	a, c, w, _ := claudeRig(t)
	var heard []error
	if err := a.SendText(AgentRef{Codename: "cl"}, "hello cl", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(c.panel.pastes) != 0 || len(c.panel.keys) != 0 || len(heard) != 1 || heard[0] == nil {
		t.Fatalf("pasted %q, pressed %q, heard %v: want nothing typed, the send ended unsent", c.panel.pastes, c.panel.keys, heard)
	}
}

// The notice is per session: the agent started again (a new session) that hasn't started either is
// told of again.
func TestTheNotStartedNoticeIsOncePerSession(t *testing.T) {
	a, c, w, instance := claudeRig(t)
	a.deliverFallback("cl", "one", func(error) {})
	settleMessaging(c, w)
	c.panel.exit(instance.tab, 0)
	settleMessaging(c, w)
	p, _ := a.profile("cl")
	again, err := a.startInstance(p)
	if err != nil || !again.running {
		t.Fatalf("the start again: %v", err)
	}
	a.deliverFallback("cl", "two", func(error) {})
	settleMessaging(c, w)
	if n := notStartedNotices(c.notes, "Claude", again.dir); n != 2 {
		t.Fatalf("notices %q: want one for each session, two", c.notes)
	}
}

// A crew Claude that exits before it started may have declined its folder-trust question: the user
// is told so, with its exit code. One that had started and exits is told nothing of the kind.
func TestAClaudeThatExitsBeforeItStartsIsToldWhy(t *testing.T) {
	a, c, w, instance := claudeRig(t)
	c.panel.exit(instance.tab, 3)
	settleMessaging(c, w)
	declined := func() int {
		n := 0
		for _, note := range c.notes {
			if strings.Contains(note, "Claude") && strings.Contains(note, "3") && strings.Contains(note, "trust") {
				n++
			}
		}
		return n
	}
	if declined() != 1 {
		t.Fatalf("notices %q: want one that Claude exited (3) before it started, maybe declining its folder-trust question", c.notes)
	}

	p, _ := a.profile("cl")
	again, err := a.startInstance(p)
	if err != nil || !again.running {
		t.Fatalf("the start again: %v", err)
	}
	w.d.started(t, "cl")
	c.panel.exit(again.tab, 3)
	settleMessaging(c, w)
	if declined() != 1 {
		t.Fatalf("notices %q: a Claude that had started is told nothing of its folder-trust question", c.notes)
	}
}
