package crew

import (
	"errors"
	"io"
	"slices"
	"testing"

	corecrew "github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// TestTheCrewHostListsOtherScopesLiveAgents (item 17): the window's crew host answers A's peer
// lookup from crew.list: each live agent of another scope, none of its own scope's; a crew.list
// that fails, or a window holding no scope, answers none.
func TestTheCrewHostListsOtherScopesLiveAgents(t *testing.T) {
	a, _, w := crewAgents(t, oneTwo)
	own, _, _ := crewOf(a).Held()
	w.crewList = func() (any, error) {
		return []any{
			map[string]any{"codename": "one", "scope": own, "presence": corecrew.PresenceLive, "reachable": true},
			map[string]any{"codename": "jarvis", "scope": "autovim:plugins", "presence": corecrew.PresenceLive, "reachable": true},
			map[string]any{"codename": "zen", "scope": "autovim:mcat", "presence": corecrew.PresenceLive, "why": "no_endpoint"},
		}, nil
	}
	var got []string
	crewOf(a).PeerAgents(func(peers []feature.AgentEntry) {
		for _, e := range peers {
			got = append(got, e.Ref.Codename+"@"+e.Peer.Scope)
		}
	})
	if !slices.Equal(got, []string{"jarvis@autovim:plugins", "zen@autovim:mcat"}) {
		t.Errorf("the host's peers %v, want the two other scopes' agents", got)
	}

	w.crewList = func() (any, error) { return nil, io.ErrUnexpectedEOF }
	asked := false
	crewOf(a).PeerAgents(func(peers []feature.AgentEntry) {
		asked = true
		if len(peers) != 0 {
			t.Errorf("a failed crew.list answered peers %v", peers)
		}
	})
	if !asked {
		t.Error("a failed crew.list never answered")
	}

	h := NewHost(&crewWindow{d: newCrewDaemonForHost()}, nil, t.TempDir(), HostHooks{}) // never connected: no scope
	asked = false
	h.PeerAgents(func(peers []feature.AgentEntry) { asked = len(peers) == 0 })
	if !asked {
		t.Error("a window holding no scope didn't answer no peers")
	}

	// Connected, but holding no scope (its claim lost): the crew would answer, and is never asked.
	crewOf(a).scope = ""
	listed := 0
	w.crewList = func() (any, error) {
		listed++
		return []any{map[string]any{"codename": "jarvis", "scope": "autovim:plugins", "presence": corecrew.PresenceLive}}, nil
	}
	asked = false
	crewOf(a).PeerAgents(func(peers []feature.AgentEntry) { asked = len(peers) == 0 })
	if !asked || listed != 0 {
		t.Errorf("a connected window holding no scope: answered none %v, crew.list asked %d times; want none, never asked", asked, listed)
	}
}

// TestTheCrewHostSendsAnotherHostsAgentATaskUnrung (item 17): the task goes to codename@scope from
// the window's scope, as the user's request, keyed as A keys it, and never rung (fire false), so it
// is queued and woken as any mail; the daemon's refusal is the caller's to hear. A window holding no
// scope sends nothing and says so.
func TestTheCrewHostSendsAnotherHostsAgentATaskUnrung(t *testing.T) {
	a, _, w := crewAgents(t, oneTwo)
	own, _, _ := crewOf(a).Held()
	var sent [][]any
	w.inboxSend = func(p []any) (any, error) {
		sent = append(sent, p)
		return map[string]any{}, nil
	}
	var heard []error
	crewOf(a).SendTask("jarvis", "autovim:plugins", "Task: t", "body", "assign:1:2", func(err error) { heard = append(heard, err) })
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
	crewOf(a).SendTask("jarvis", "autovim:plugins", "Task: t", "body", "assign:1:3", func(err error) { heard = append(heard, err) })
	if len(heard) != 1 || !errors.Is(heard[0], refused) {
		t.Errorf("a refused send was heard as %v", heard)
	}

	h := NewHost(&crewWindow{d: newCrewDaemonForHost()}, nil, t.TempDir(), HostHooks{})
	heard = nil
	h.SendTask("jarvis", "autovim:plugins", "Task: t", "body", "k", func(err error) { heard = append(heard, err) })
	if len(heard) != 1 || !errors.Is(heard[0], agents.ErrDoorbellNotSent) {
		t.Errorf("a window holding no scope heard %v, want a send it couldn't make", heard)
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
