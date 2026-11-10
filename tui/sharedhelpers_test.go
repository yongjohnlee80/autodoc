package tui

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// Helpers the cells share, whatever OS they run on: what a screen shows and which panel has the
// keyboard. The cells that start a real shell or a fake agent CLI keep their own files.

// agentConfig is the config the agent tests' TUI runs on: not the default, and one word only quoted.
const agentConfig = "/a config's dir/autodoc.toml"

func (r *running) noticed(text string) bool {
	return onLoop(r, func() bool {
		for _, n := range r.h.noticeLog.Items {
			if strings.Contains(n.Text, text) {
				return true
			}
		}
		return false
	})
}

func (r *running) focusedOn(id string) bool { return onLoop(r, func() bool { return r.h.within(id) }) }

func (r *running) panelIsOpen(id string) bool {
	return onLoop(r, func() bool { return r.h.panelOpen[id] })
}

func (r *running) termShown() bool {
	return onLoop(r, func() bool { return r.h.panelOpen["terminal"] })
}

// frameAt is the row and column of the terminal's frame corner, -1s when it is not on screen.
func frameAt(screen string) (row, column int) {
	for y, l := range strings.Split(screen, "\n") {
		if c := col(l, "┌ terminal"); c >= 0 {
			return y, c
		}
	}
	return -1, -1
}

func maximizedNow(maximized func() bool, want bool) func(string) bool {
	return func(string) bool { return maximized() == want }
}

// covered is whether panel id is open but covered by a window or panel over it; read on the loop.
func (r *running) covered(id string) bool {
	return onLoop(r, func() bool { return r.h.panelCovered(id) })
}

// cellOf is where text first shows on the screen sc: its column and row.
func cellOf(t *testing.T, sc, text string) (x, y int) {
	t.Helper()
	for row, line := range strings.Split(sc, "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return utf8.RuneCountInString(line[:i]), row
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", text, sc)
	return 0, 0
}

// windowControl presses a panel's strip control, "_", "+" or "x", at the strip's right end: the first
// strip on screen with them.
func windowControl(t *testing.T, r *running, which string) {
	t.Helper()
	x, y := cellOf(t, r.s.String(), "_  +  x") // side by side, three cells apart
	x += map[string]int{"_": 0, "+": 3, "x": 6}[which]
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
}

// fakeTerminal is an agent CLI's process: Wait returns once ended, Close records the terminal closed.
type fakeTerminal struct {
	terminal.TerminalProcess
	ended  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{ended: make(chan struct{}), closed: make(chan struct{})}
}

func (f *fakeTerminal) Close() error { f.once.Do(func() { close(f.closed) }); return nil }

func (f *fakeTerminal) Wait() (int, error) { <-f.ended; return 0, nil }

func (f *fakeTerminal) Resize(_, _ int) error { return nil }

func (r *running) agentRunningNow() bool {
	return onLoop(r, func() bool {
		for _, entry := range r.h.agents.AgentEntries() {
			if entry.Ref.Codename == r.h.agents.selectedAgent {
				return entry.Running
			}
		}
		return false
	})
}

// switchTo enters ws and waits for it.
func (r *running) switchTo(t *testing.T, ws string) {
	t.Helper()
	r.h.p.Post(func() { r.h.enter(ws) })
	r.s.WaitFor(t, ws+" entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == ws }) })
}

// shellsRunning is how many of the terminal's shells run.
func (r *running) shellsRunning() int {
	n := 0
	for _, tab := range r.shellTabs() {
		if tab.Running {
			n++
		}
	}
	return n
}

// shellTabs are the terminal panel's tabs, a snapshot taken on the loop.
func (r *running) shellTabs() []terminal.TerminalTab {
	return onLoop(r, func() []terminal.TerminalTab {
		c, _ := r.h.p.Find("terminalView")
		tabs, ok := tuidecl.TerminalTabsOf(c)
		if !ok {
			return nil
		}
		return tabs.Tabs()
	})
}
