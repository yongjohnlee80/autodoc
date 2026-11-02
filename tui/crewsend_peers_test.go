package tui

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// TestTheCrewHostListsOtherScopesLiveAgents (item 17): the window's crew host answers A's peer
// lookup from crew.list: each live agent of another scope, none of its own scope's; a crew.list
// that fails, or a window holding no scope, answers none.
func TestTheCrewHostListsOtherScopesLiveAgents(t *testing.T) {
	a, _, w := crewAgents(t, oneTwo)
	own, _, _ := a.crew.held()
	w.crewList = func() (any, error) {
		return []any{
			map[string]any{"codename": "one", "scope": own, "presence": crew.PresenceLive, "reachable": true},
			map[string]any{"codename": "jarvis", "scope": "autovim:plugins", "presence": crew.PresenceLive, "reachable": true},
			map[string]any{"codename": "zen", "scope": "autovim:mcat", "presence": crew.PresenceLive, "why": "no_endpoint"},
		}, nil
	}
	var got []string
	a.crew.peerAgents(func(peers []AgentEntry) {
		for _, e := range peers {
			got = append(got, e.Ref.Codename+"@"+e.Peer.Scope)
		}
	})
	if !slices.Equal(got, []string{"jarvis@autovim:plugins", "zen@autovim:mcat"}) {
		t.Errorf("the host's peers %v, want the two other scopes' agents", got)
	}

	w.crewList = func() (any, error) { return nil, io.ErrUnexpectedEOF }
	asked := false
	a.crew.peerAgents(func(peers []AgentEntry) {
		asked = true
		if len(peers) != 0 {
			t.Errorf("a failed crew.list answered peers %v", peers)
		}
	})
	if !asked {
		t.Error("a failed crew.list never answered")
	}

	h := newCrewHost(&crewWindow{d: newCrewDaemonForHost()}, t.TempDir()) // never connected: no scope
	asked = false
	h.peerAgents(func(peers []AgentEntry) { asked = len(peers) == 0 })
	if !asked {
		t.Error("a window holding no scope didn't answer no peers")
	}
}

// TestTheCrewHostSendsAnotherHostsAgentATaskUnrung (item 17): the task goes to codename@scope from
// the window's scope, as the user's request, keyed as A keys it, and never rung (fire false), so it
// is queued and woken as any mail; the daemon's refusal is the caller's to hear. A window holding no
// scope sends nothing and says so.
func TestTheCrewHostSendsAnotherHostsAgentATaskUnrung(t *testing.T) {
	a, _, w := crewAgents(t, oneTwo)
	own, _, _ := a.crew.held()
	var sent [][]any
	w.inboxSend = func(p []any) (any, error) {
		sent = append(sent, p)
		return map[string]any{}, nil
	}
	var heard []error
	a.crew.sendTask("jarvis", "autovim:plugins", "Task: t", "body", "assign:1:2", func(err error) { heard = append(heard, err) })
	if len(sent) != 1 || len(heard) != 1 || heard[0] != nil {
		t.Fatalf("sent %v, heard %v; want one send, answered ok", sent, heard)
	}
	p := sent[0]
	if p[0] != own || !slices.Equal(p[3].([]any), []any{"jarvis@autovim:plugins"}) || p[4] != "body" || p[5] != "assign:1:2" ||
		p[6] != mail.KindRequest || p[7] != "Task: t" || p[8] != false {
		t.Errorf("inbox.send %v: want from %s to jarvis@autovim:plugins, the request keyed assign:1:2, unrung", p, own)
	}

	refused := errors.New("refused")
	w.inboxSend = func([]any) (any, error) { return nil, refused }
	heard = nil
	a.crew.sendTask("jarvis", "autovim:plugins", "Task: t", "body", "assign:1:3", func(err error) { heard = append(heard, err) })
	if len(heard) != 1 || !errors.Is(heard[0], refused) {
		t.Errorf("a refused send was heard as %v", heard)
	}

	h := newCrewHost(&crewWindow{d: newCrewDaemonForHost()}, t.TempDir())
	heard = nil
	h.sendTask("jarvis", "autovim:plugins", "Task: t", "body", "k", func(err error) { heard = append(heard, err) })
	if len(heard) != 1 || !errors.Is(heard[0], errDoorbellNotSent) {
		t.Errorf("a window holding no scope heard %v, want a send it couldn't make", heard)
	}
}

// TestTodosAssignPreviewsAnotherHostsAgent (item 17): another host's agent's preview is titled by
// codename@project and names its scope and whether a wake reaches it.
func TestTodosAssignPreviewsAnotherHostsAgent(t *testing.T) {
	c, _ := newPeerCore("autodoc:0123456789abcdef", peer("zen", "autovim:mcat", "mcat", false, "no_endpoint"))
	v := newTodos(c)
	e := c.peers[0]
	pn := v.assignPick.preview(e)(context.Background()).v
	if pn.title != "zen@mcat" || !strings.Contains(pn.text, "autovim:mcat") || !strings.Contains(pn.text, runState(c, e)) {
		t.Errorf("the preview %q / %q: want zen@mcat, its scope and its state", pn.title, pn.text)
	}
}

// TestTodosAssignSendsNoPeerTaskFromAWindowWithoutACrew: a hand-over to another host's agent on a
// window that can't reach the crew sends nothing and says nothing.
func TestTodosAssignSendsNoPeerTaskFromAWindowWithoutACrew(t *testing.T) {
	ac, _ := assigningStore(AgentEntry{Ref: AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true})
	v := newTodos(ac)
	v.list()
	s, _ := v.cursor()
	before := len(ac.notes)
	v.sendToPeer(&todoAssign{store: s, agent: peer("jarvis", "autovim:plugins", "plugins", true, "")}, s, map[string]any{"id": "t1"}, "Go.")
	if len(ac.notes) != before {
		t.Errorf("a window without a crew noticed %v", ac.notes[before:])
	}
}
