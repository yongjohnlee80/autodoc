//go:build linux || darwin

package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// recordingShell is a user's shell that, its tty raw, appends every byte it is sent to report: every
// terminal tab runs one, so report is what any tab's program got.
func recordingShell(t *testing.T) (shell, report string) {
	t.Helper()
	dir := t.TempDir()
	shell, report = filepath.Join(dir, "recording-shell"), filepath.Join(dir, "report")
	script := "#!/bin/sh\nstty raw -echo\nprintf 'ready\\r\\n'\nexec cat >> " + shellQuote(report) + "\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return shell, report
}

func ctrlTab(back bool) tui.KeyEvent {
	mods := tui.ModCtrl
	if back {
		mods |= tui.ModShift
	}
	return tui.KeyEvent{Kind: tui.KeyPress, Code: tui.KeyTab, Mods: mods}
}

// TestCtrlTabMovesBetweenTheTerminalsTabs (v0.3.1): three shells, the newest's tab showing.
// Ctrl+Tab goes round to the first, Ctrl+Shift+Tab back to the last, each tab keeping the keyboard,
// in input and Normal mode alike, and no program gets either key. Ctrl+\ then Ctrl+Tab sends it to
// the shell, and the tab stays. With one shell, or the keyboard on the page, it does nothing (the
// command run on the loop: a key that does nothing has no effect to wait on).
func TestCtrlTabMovesBetweenTheTerminalsTabs(t *testing.T) {
	shell, report := recordingShell(t)
	_, r := runTUIWithShell(t, shell, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	if moved := onLoop(r, func() bool {
		p, _ := r.h.terminals.panel()
		was := p.Active()
		r.h.cyclePanelTab(1)
		return p.Active() != was || !r.h.paneHasKeyboard("terminalView") // on the loop already: never r.focused
	}); moved {
		t.Fatal("Ctrl+Tab with one shell moved the tab or the keyboard")
	}
	onLoop(r, func() bool { r.h.terminals.newShell(); r.h.terminals.newShell(); return true })
	tabs := func() []widget.TerminalTabID {
		return onLoop(r, func() []widget.TerminalTabID {
			p, _ := r.h.terminals.panel()
			var ids []widget.TerminalTabID
			for _, tab := range p.Tabs() {
				ids = append(ids, tab.ID)
			}
			return ids
		})
	}
	active := func() widget.TerminalTabID {
		return onLoop(r, func() widget.TerminalTabID { p, _ := r.h.terminals.panel(); return p.Active() })
	}
	r.s.WaitFor(t, "three shells, the newest showing", func(string) bool {
		ids := tabs()
		return len(ids) == 3 && active() == ids[2] && r.focused("terminalView")
	})
	ids := tabs()
	to := func(key tui.KeyEvent, want widget.TerminalTabID, what string) {
		t.Helper()
		r.keys(t, key)
		r.s.WaitFor(t, what, func(string) bool { return active() == want && r.focused("terminalView") })
	}
	to(ctrlTab(false), ids[0], "Ctrl+Tab round to the first shell")
	to(ctrlTab(false), ids[1], "Ctrl+Tab to the second")
	to(ctrlTab(true), ids[0], "Ctrl+Shift+Tab back to the first")
	to(ctrlTab(true), ids[2], "Ctrl+Shift+Tab round to the last")
	r.keys(t, decltest.Ctrl('t'))                         // Normal mode
	to(ctrlTab(false), ids[0], "Ctrl+Tab in Normal mode") // the last's mode is its own: the first's is input
	r.keys(t, decltest.Type("x")...)
	r.s.WaitFor(t, "x typed", func(string) bool { return len(reported(report)) > 0 })
	if got := reported(report); got != "x" {
		t.Fatalf("the shells got %q, want only the x typed: Ctrl+Tab reached a program", got)
	}
	r.keys(t, decltest.Ctrl('\\'), ctrlTab(false))
	r.s.WaitFor(t, "Ctrl+Tab sent through", func(string) bool { return len(reported(report)) > 1 })
	if active() != ids[0] {
		t.Error("Ctrl+\\ then Ctrl+Tab moved the tab")
	}
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	if moved := onLoop(r, func() bool { r.h.cyclePanelTab(1); p, _ := r.h.terminals.panel(); return p.Active() != ids[0] }); moved {
		t.Error("Ctrl+Tab with the keyboard on the page moved the terminal's tab")
	}
}

// TestCtrlTabMovesBetweenTheAgentsTabs: two agents running, the second's tab showing. Ctrl+Tab goes
// round to the first, as Go to › would (the agents' selected agent follows), and Ctrl+Shift+Tab
// back.
func TestCtrlTabMovesBetweenTheAgentsTabs(t *testing.T) {
	agent, _ := recordingAgent(t, true)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{
		{Name: "one", Command: shellQuote(agent), Codename: "one"},
		{Name: "two", Command: shellQuote(agent), Codename: "two"},
	}, "one")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "one running", func(string) bool { return onLoop(r, func() bool { return r.h.agents.agentRunning == "one" }) })
	onLoop(r, func() bool { r.h.agents.openAgentProfiles(); r.h.agents.useAgent(1); return true })
	selected := func() string { return onLoop(r, func() string { return r.h.agents.selectedAgent }) }
	r.s.WaitFor(t, "two running, its tab showing", func(string) bool { return selected() == "two" && r.focused("agentView") })
	r.keys(t, ctrlTab(false))
	r.s.WaitFor(t, "Ctrl+Tab round to one", func(string) bool { return selected() == "one" && r.focused("agentView") })
	r.keys(t, ctrlTab(true))
	r.s.WaitFor(t, "Ctrl+Shift+Tab back to two", func(string) bool { return selected() == "two" && r.focused("agentView") })
}

// TestNextTabGoesRoundPastClosingTabs: the tab a step away, round both ways; a tab closing is passed
// over; with fewer than two open, or the active one closing, none.
func TestNextTabGoesRoundPastClosingTabs(t *testing.T) {
	tab := func(id string, closing bool) widget.TerminalTab {
		return widget.TerminalTab{ID: widget.TerminalTabID(id), Closing: closing}
	}
	abc := []widget.TerminalTab{tab("a", false), tab("b", true), tab("c", false), tab("d", false)}
	for _, c := range []struct {
		tabs   []widget.TerminalTab
		active string
		step   int
		want   string
	}{
		{abc, "a", 1, "c"},  // b closing: passed over
		{abc, "d", 1, "a"},  // round
		{abc, "a", -1, "d"}, // round back
		{abc, "c", -1, "a"}, // back past b
		{abc, "b", 1, ""},   // the active one closing: none
		{abc, "z", 1, ""},   // the active one gone: none
		{[]widget.TerminalTab{tab("a", false)}, "a", 1, ""},                 // one tab
		{[]widget.TerminalTab{tab("a", false), tab("b", true)}, "a", 1, ""}, // one open
	} {
		got, ok := tabAfter(c.tabs, widget.TerminalTabID(c.active), c.step)
		if string(got) != c.want || ok != (c.want != "") {
			t.Errorf("tabAfter(%s, %+d) = %q %v, want %q", c.active, c.step, got, ok, c.want)
		}
	}
}
