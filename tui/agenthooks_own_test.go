package tui

import (
	"reflect"
	"testing"

	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// TestEachProgramHasItsOwnAgentHooks: two programs made from the same seams each read their own
// copy of the hooks an agent's start reads off the loop; setting one program's leaves the other's,
// and the seams they were made from, as they were.
func TestEachProgramHasItsOwnAgentHooks(t *testing.T) {
	first := func(pty.Cmd) (terminal.TerminalProcess, error) { return nil, nil }
	second := func(pty.Cmd) (terminal.TerminalProcess, error) { return nil, nil }
	here := func([]string, ...string) error { return nil }
	s := defaultSeams
	s.setAgentSpawn(first)
	s.setAgentCodexHere(here)
	s.setAgentBin("/build/one")
	a := newHost(NewSession("unused", nil), Options{seams: &s})
	b := newHost(NewSession("unused", nil), Options{seams: &s})
	if a.seams.live == nil || b.seams.live == nil || a.seams.live == b.seams.live {
		t.Fatal("the two programs share their hooks, or have none of their own")
	}
	same := func(f, g any) bool { return reflect.ValueOf(f).Pointer() == reflect.ValueOf(g).Pointer() }
	a.seams.setAgentSpawn(second)
	a.seams.setAgentCodexHere(nil)
	a.seams.setAgentBin("/build/two")
	if !same(a.seams.Spawn(), second) || a.seams.CodexHere() != nil || a.seams.AgentBin() != "/build/two" {
		t.Fatal("the program does not read the hooks set on it")
	}
	if !same(b.seams.Spawn(), first) || !same(b.seams.CodexHere(), here) || b.seams.AgentBin() != "/build/one" {
		t.Fatal("setting one program's hooks changed the other's")
	}
	if s.live != nil || !same(s.Spawn(), first) || s.AgentBin() != "/build/one" {
		t.Fatal("setting a program's hooks changed the seams it was made from")
	}
}
