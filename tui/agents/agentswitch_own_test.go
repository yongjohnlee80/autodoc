//go:build linux || darwin

package agents

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// TestTheSwitchIsCoalesced (docs/decisions/crew.md#one-coalesced-switch-per-agent): an agent is
// told one switch from the workspace it last saw to the one in use, however many came between. kb →
// notes → other is one, kb → other; back to the one it saw is none; a rename (the same root) is
// renamed; a name the TUI doesn't list yet (its root unknown) waits; no workspace is left; a
// restart (a new token) starts again from its own. Each workspace in use is handed as the host
// knows it (Host.wsRefOf).
func TestTheSwitchIsCoalesced(t *testing.T) {
	a := &Agents{instances: map[string]*AgentInstance{
		"zen": {token: "t1", workspace: feature.Workspace{Name: "kb", Root: "/k"}},
	}}
	none := func(string) string { return "" }
	take := func(name, root string) (WsSwitch, bool) { return a.TakeSwitch("zen", WsRef{name, root}, none) }

	if sw, ok := take("kb", "/k"); ok {
		t.Errorf("in its own workspace: %+v", sw)
	}
	if sw, ok := take("other", "/o"); !ok || sw != (WsSwitch{Kind: SwitchEntered, From: WsRef{"kb", "/k"}, To: WsRef{"other", "/o"}}) {
		t.Errorf("kb → notes → other: %+v %v, want one kb → other", sw, ok)
	}
	if sw, ok := take("other", "/o"); ok {
		t.Errorf("asked again: %+v", sw)
	}
	if sw, ok := take("other", "/o"); ok {
		t.Errorf("other → notes → other: %+v", sw)
	}
	if sw, ok := take("renamed", "/o"); !ok || sw != (WsSwitch{Kind: switchRenamed, From: WsRef{"other", "/o"}, To: WsRef{"renamed", "/o"}}) {
		t.Errorf("other renamed: %+v %v", sw, ok)
	}
	if sw, ok := take("ghost", ""); ok {
		t.Errorf("a workspace the list doesn't hold yet: %+v", sw)
	}
	if sw, ok := take("ghost", "/g"); !ok || sw.From != (WsRef{"renamed", "/o"}) || sw.Kind != SwitchEntered {
		t.Errorf("once listed: %+v %v, want renamed → ghost", sw, ok)
	}
	if sw, ok := take("", ""); !ok || sw.Kind != switchLeft || sw.To != (WsRef{}) {
		t.Errorf("no workspace: %+v %v", sw, ok)
	}
	a.instances["zen"].token = "t2" // restarted in its tab: it knows only kb
	if sw, ok := take("ghost", "/g"); !ok || sw.From != (WsRef{"kb", "/k"}) {
		t.Errorf("after a restart: %+v %v, want from kb", sw, ok)
	}
	if _, ok := a.TakeSwitch("nobody", WsRef{}, none); ok {
		t.Error("an agent with no instance was told a switch")
	}

	spawn := WsRef{"kb", "/k"}
	for _, c := range []struct {
		sw   WsSwitch
		want []string
	}{
		{WsSwitch{Kind: SwitchEntered, From: spawn, To: WsRef{"notes", "/n"}, Document: "b.md"}, []string{"moved to workspace notes (/n), from kb (/k); open: b.md", "You stay in kb"}},
		{WsSwitch{Kind: switchRenamed, From: spawn, To: WsRef{"kb2", "/k"}}, []string{"kb was renamed kb2", "$AUTODOC_WORKSPACE still says kb"}},
		{WsSwitch{Kind: switchLeft, From: spawn}, []string{"no workspace open", "You stay in kb"}},
	} {
		n := c.sw.notice(spawn)
		for _, w := range c.want {
			if !strings.HasPrefix(n, "[AutoDoc] ") || strings.ContainsAny(n, "\r\n") || !strings.Contains(n, w) {
				t.Errorf("%s's notice %q lacks %q", c.sw.Kind, n, w)
			}
		}
	}
}
