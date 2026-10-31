//go:build linux || darwin

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// keyAgent is an agent CLI that, its tty raw, prints the code of every byte it is sent, key-<n>: a
// test sees exactly which keys reached the program, the control ones included.
func keyAgent(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key-agent")
	script := `#!/bin/sh
stty raw -echo
printf 'ready\r\n'
while :; do
  c=$(dd bs=1 count=1 2>/dev/null)
  if [ -z "$c" ]; then printf 'key-10\r\n'; continue; fi
  n=$(printf '%s' "$c" | od -An -tu1 | tr -d ' \n')
  printf 'key-%s\r\n' "$n"
done
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// runKeyAgent runs the agents panel, docked at the bottom, on keyAgent, its keyboard there, typing.
func runKeyAgent(t *testing.T) *running {
	t.Helper()
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(keyAgent(t)), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitForText(t, "ready")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	return r
}

// got waits for the agent to print key-n.
func (r *running) got(t *testing.T, n int) {
	t.Helper()
	r.s.WaitForText(t, fmt.Sprintf("key-%d", n))
}

// TestTheMoveKeysLeaveATypingAgent (v0.3.1): typing in an agent docked below the page, Ctrl+K moves
// the keyboard up to the page, and the agent gets none of it. Ctrl+J moves back. Ctrl+\ then Ctrl+L
// sends the agent ^L, and Ctrl+\ then Ctrl+J its newline, the keyboard staying. Backspace is still
// the program's backspace.
func TestTheMoveKeysLeaveATypingAgent(t *testing.T) {
	r := runKeyAgent(t)
	r.keys(t, decltest.Ctrl('k'))
	r.s.WaitFor(t, "the keyboard on the page", func(string) bool { return !r.focused("agentView") })
	r.keys(t, decltest.Ctrl('j'))
	r.s.WaitFor(t, "the keyboard back in the agent", func(string) bool { return r.focused("agentView") })
	r.keys(t, decltest.Type("a")...)
	r.got(t, 'a')
	if strings.Contains(r.s.String(), "key-11") || strings.Contains(r.s.String(), "key-10") {
		t.Fatalf("a move key reached the agent:\n%s", r.s.String())
	}
	r.keys(t, decltest.Ctrl('\\'), decltest.Ctrl('l'))
	r.got(t, 12)
	r.keys(t, decltest.Ctrl('\\'), decltest.Ctrl('j'))
	r.got(t, 10)
	if !r.focused("agentView") {
		t.Error("the keyboard left the agent on a key sent through")
	}
	if strings.Contains(r.s.String(), "key-28") {
		t.Errorf("Ctrl+\\ itself reached the agent:\n%s", r.s.String())
	}
	r.keys(t, tui.KeyEvent{Kind: tui.KeyPress, Code: tui.KeyBackspace})
	r.got(t, 127)
}

// TestThePreferenceOffGivesTheProgramTheMoveKeys: Options › Terminal's preference off, the four keys
// are the program's again, live: Ctrl+K reaches the agent and the keyboard stays; on again, it moves.
func TestThePreferenceOffGivesTheProgramTheMoveKeys(t *testing.T) {
	r := runKeyAgent(t)
	onLoop(r, func() bool { r.h.toggleTerminalPanelKeys(); return true })
	r.s.WaitFor(t, "the preference off", func(string) bool { return onLoop(r, func() bool { return !r.h.prefs.termPanelKeys }) })
	r.keys(t, decltest.Ctrl('k'))
	r.got(t, 11)
	if !r.focused("agentView") {
		t.Error("with the preference off, Ctrl+K moved the keyboard")
	}
	onLoop(r, func() bool { r.h.toggleTerminalPanelKeys(); return true })
	r.s.WaitFor(t, "the preference on", func(string) bool { return onLoop(r, func() bool { return r.h.prefs.termPanelKeys }) })
	r.keys(t, decltest.Ctrl('k'))
	r.s.WaitFor(t, "the keyboard on the page", func(string) bool { return !r.focused("agentView") })
}

// TestAMoveArrivesReadyToType (v0.3.1): the agent's tab in Normal mode, a Ctrl-move from the
// page arrives in input mode, and the next key is the program's. Arriving another way (a click, SPC,
// reopening: focusWindow) keeps the tab's mode.
func TestAMoveArrivesReadyToType(t *testing.T) {
	r := runKeyAgent(t)
	var mu sync.Mutex
	var modes []widget.TerminalMode
	onLoop(r, func() bool {
		c, _ := r.h.p.Find("agentView")
		tabs, _ := tuidecl.TerminalTabsOf(c)
		tabs.SetOnMode(func(_ widget.TerminalTabID, m widget.TerminalMode) { mu.Lock(); modes = append(modes, m); mu.Unlock() })
		return true
	})
	last := func() (widget.TerminalMode, int) {
		mu.Lock()
		defer mu.Unlock()
		if len(modes) == 0 {
			return widget.TerminalInput, 0
		}
		return modes[len(modes)-1], len(modes)
	}
	r.keys(t, decltest.Ctrl('t')) // the mode key: Normal mode
	r.s.WaitFor(t, "normal mode", func(string) bool { m, _ := last(); return m == widget.TerminalNormal })
	r.keys(t, decltest.Ctrl('k'))
	r.s.WaitFor(t, "the keyboard on the page", func(string) bool { return !r.focused("agentView") })
	r.keys(t, decltest.Ctrl('j'))
	r.s.WaitFor(t, "arrived in input mode", func(string) bool { m, _ := last(); return m == widget.TerminalInput && r.focused("agentView") })
	r.keys(t, decltest.Type("z")...)
	r.got(t, 'z')

	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "normal mode again", func(string) bool { m, _ := last(); return m == widget.TerminalNormal })
	_, n := last()
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the keyboard on the page", func(string) bool { return !r.focused("agentView") })
	onLoop(r, func() bool { r.h.focusWindow("agent"); return true })
	r.s.WaitFor(t, "the agent focused", func(string) bool { return r.focused("agentView") })
	if m, after := last(); m != widget.TerminalNormal || after != n {
		t.Errorf("arriving by focusWindow changed the mode: %v (%d changes, %d before)", m, after, n)
	}
}
