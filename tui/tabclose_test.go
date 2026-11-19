//go:build linux || darwin

// Unix only: these cells run a real shell and real programs in it (sleep, read), whose foreground
// the terminal reads to tell a busy shell from one at its prompt.

package tui

import (
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// THE TABS' ×: a shell tab's × is its menu's Close (closeShell), asked first while a program runs in
// the shell, or while the terminal can't tell, and at once at an idle prompt.

// pressTabX presses the × of the tab named label and lets it go there, as a click does.
func (r *running) pressTabX(t *testing.T, label string) {
	t.Helper()
	r.s.WaitForText(t, "× "+label)
	x, y := r.textAt("× " + label)
	r.keys(t, mouse(tuicore.MousePress, x, y), mouse(tuicore.MouseRelease, x, y))
}

// shellActivity is what the terminal says tab id's shell is doing.
func (r *running) shellActivity(id terminal.TerminalTabID) terminal.TerminalActivity {
	return onLoop(r, func() terminal.TerminalActivity {
		panel, err := r.h.terminals.panel()
		if err != nil {
			return terminal.TerminalActivityUnknown
		}
		return panel.Activity(id)
	})
}

// openTerminal opens the terminal on its first shell, with the keyboard in it.
func (r *running) openTerminalShell(t *testing.T) terminal.TerminalTabID {
	t.Helper()
	r.leader(t, '`')
	r.s.WaitForText(t, "┌ terminal")
	waitShellStarted(t, r, 1)
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	return r.shellTabs()[0].ID
}

// liveLabels are the tabs not closing, by label.
func (r *running) liveLabels() []string {
	var out []string
	for _, tab := range r.shellTabs() {
		if !tab.Closing {
			out = append(out, tab.Label)
		}
	}
	return out
}

// A shell at its prompt closes from its tab's × at once, unasked; the last one leaves the panel open
// on its empty text, as the menu's Close does. The mutant, named here: the × asking whatever the
// shell is doing.
func TestATabsXClosesAShellAtItsPromptAtOnce(t *testing.T) {
	t.Parallel()
	_, r := runShellTUI(t, nil)
	first := r.openTerminalShell(t)
	onLoop(r, func() bool { r.h.terminals.newShell(); return true })
	waitShellStarted(t, r, 2)
	second := r.shellTabs()[1].ID
	r.s.WaitFor(t, "the second shell at its prompt", func(string) bool { return r.shellActivity(second) == terminal.TerminalIdle })

	r.pressTabX(t, "shell 2")
	r.s.WaitFor(t, "shell 2 gone", func(string) bool { return slices.Equal(r.liveLabels(), []string{"shell"}) })
	if strings.Contains(r.s.String(), "close the shell?") {
		t.Fatal("the × asked before closing a shell at its prompt")
	}

	r.s.WaitFor(t, "the first shell at its prompt", func(string) bool { return r.shellActivity(first) == terminal.TerminalIdle })
	r.pressTabX(t, "shell")
	r.s.WaitForText(t, "No shell running")
	if !r.termShown() {
		t.Fatal("the last tab's × closed the panel")
	}
}

// The × of a background tab closes it and leaves the keyboard in the active shell: the press on the
// × neither chooses its tab nor takes the keyboard from the shell that had it. The mutant, named
// here: the × press leaving the keyboard on the strip (the keys typed next go to the page).
func TestABackgroundTabsXLeavesTheKeyboardInTheActiveShell(t *testing.T) {
	t.Parallel()
	_, r := runShellTUI(t, nil)
	first := r.openTerminalShell(t)
	onLoop(r, func() bool { r.h.terminals.newShell(); return true })
	waitShellStarted(t, r, 2)
	r.s.WaitFor(t, "the new shell's keyboard", func(string) bool { return r.focused("terminalView") })
	r.s.WaitFor(t, "the first shell at its prompt", func(string) bool { return r.shellActivity(first) == terminal.TerminalIdle })
	r.pressTabX(t, "shell")
	r.s.WaitFor(t, "the first shell gone", func(string) bool { return slices.Equal(r.liveLabels(), []string{"shell 2"}) })
	r.keys(t, decltest.Type("echo still-$((40+2))\r")...)
	r.s.WaitForText(t, "still-42")
}

// A shell running a program is asked about first, from its tab's × and its menu's Close alike:
// No leaves everything as it was (the program running, the tab there, the keyboard in the shell),
// and Yes hangs it up. The mutants, named here: the × hanging up without the question, and No
// hanging up all the same.
func TestATabsXAsksWhileAProgramRunsAndNoLeavesItAll(t *testing.T) {
	t.Parallel()
	_, r := runShellTUI(t, nil)
	id := r.openTerminalShell(t)
	r.keys(t, decltest.Type("sleep 300\r")...)
	r.s.WaitFor(t, "sleep in the foreground", func(string) bool { return r.shellActivity(id) == terminal.TerminalRunning })

	r.pressTabX(t, "shell")
	r.s.WaitForText(t, "close the shell?")
	r.s.WaitForText(t, "The program running in it is stopped.")
	r.keys(t, enter()) // No, the default
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "close the shell?") })
	if got := r.liveLabels(); !slices.Equal(got, []string{"shell"}) {
		t.Fatalf("No left tabs %v; want the shell's", got)
	}
	if a := r.shellActivity(id); a != terminal.TerminalRunning {
		t.Fatalf("No left the shell %v; want its program still running", a)
	}
	// the keyboard is back in the shell: what's typed next reaches it (Ctrl+C ends the sleep)
	r.keys(t, decltest.Ctrl('c'))
	r.keys(t, decltest.Type("echo back-$((40+2))\r")...)
	r.s.WaitForText(t, "back-42")
	r.keys(t, decltest.Type("sleep 300\r")...)
	r.s.WaitFor(t, "sleep again", func(string) bool { return r.shellActivity(id) == terminal.TerminalRunning })

	// the menu's Close is the same path: asked, and Yes hangs it up
	rows := onLoop(r, func() []widgetcore.MenuItemModel { return r.h.terminals.tabRows(id) })
	row, ok := menuRow(rows, "terminal.close")
	if !ok {
		t.Fatal("no Close on the shell's menu")
	}
	onLoop(r, func() bool { row.Action.(terminal.TerminalTabsMenuAction).Run(id); return true })
	r.s.WaitForText(t, "close the shell?")
	r.keys(t, key('y'))
	r.s.WaitForText(t, "No shell running")
}

// The program ending while the question is up: Yes then closes a shell at its prompt, once, with no
// second question.
func TestAProgramEndingWhileItsCloseIsAskedClosesItOnce(t *testing.T) {
	t.Parallel()
	_, r := runShellTUI(t, nil)
	id := r.openTerminalShell(t)
	// an external program waiting for a line: a builtin such as read runs in the shell itself, which
	// the terminal rightly reads as idle
	r.keys(t, decltest.Type("head -n 1 > /dev/null\r")...)
	r.s.WaitFor(t, "head in the foreground", func(string) bool { return r.shellActivity(id) == terminal.TerminalRunning })
	r.pressTabX(t, "shell")
	r.s.WaitForText(t, "close the shell?")
	// the line read ends the program behind the question (sent to the shell, the question keeping the keys)
	onLoop(r, func() bool {
		panel, err := r.h.terminals.panel()
		if err == nil {
			err = panel.SendText(id, "\n")
		}
		if err != nil {
			t.Error(err)
		}
		return true
	})
	r.s.WaitFor(t, "the shell back at its prompt", func(string) bool { return r.shellActivity(id) == terminal.TerminalIdle })
	r.keys(t, key('y'))
	r.s.WaitForText(t, "No shell running")
	if strings.Contains(r.s.String(), "close the shell?") {
		t.Fatal("a second question came after Yes")
	}
}

// A shell the terminal can't read (Windows' ConPTY, say) is asked about as a busy one, saying a
// program may be running: never closed unasked on a guess.
func TestAShellTheTerminalCannotReadIsAskedAsBusy(t *testing.T) {
	t.Parallel()
	_, r := runShellTUI(t, nil)
	r.openTerminalShell(t)
	onLoop(r, func() bool {
		r.h.terminals.activity = func(terminal.TerminalTabID) terminal.TerminalActivity { return terminal.TerminalActivityUnknown }
		return true
	})
	r.pressTabX(t, "shell")
	r.s.WaitForText(t, "A program may be running in it")
	r.keys(t, esc())
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "close the shell?") })
	if got := r.liveLabels(); !slices.Equal(got, []string{"shell"}) {
		t.Fatalf("Esc left tabs %v; want the shell's", got)
	}
}

// A Lua shell's tab follows the same rule: at its prompt (luaShell.Activity), its × closes it at
// once, unasked.
func TestALuaShellsXClosesItAtItsPromptAtOnce(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	if err := r.luaRun("terminal"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "a shell", func(string) bool { return len(r.shellTabs()) == 1 })
	onLoop(r, func() bool { r.h.terminals.newLuaShell(); return true })
	r.s.WaitFor(t, "a Lua shell", func(string) bool { return slices.Equal(r.liveLabels(), []string{"shell", "lua"}) })
	r.s.WaitForText(t, "AutoDoc's engine")
	lua := r.shellTabs()[1].ID
	r.s.WaitFor(t, "the Lua shell at its prompt", func(string) bool { return r.shellActivity(lua) == terminal.TerminalIdle })
	r.pressTabX(t, "lua")
	r.s.WaitFor(t, "the Lua shell gone", func(string) bool { return slices.Equal(r.liveLabels(), []string{"shell"}) })
	if strings.Contains(r.s.String(), "close the shell?") {
		t.Fatal("the × asked before closing a Lua shell at its prompt")
	}
}

// A job behind the shell's prompt, running in the background or stopped, is asked about: closing the
// tab would end it, which the terminal counts as running (golib's TerminalTabs.Activity reads the
// shell's children). The stopped one is stopped by kill -STOP: Ctrl+Z never reaches a shell's
// program here (ctrlz_test.go), but a program can still stop, and a job be left stopped.
func TestAShellWithAJobBehindItsPromptIsAsked(t *testing.T) {
	for _, job := range []struct{ name, line string }{
		{"background", "sleep 300 & echo job-$((40+2))\r"},
		{"stopped", "sleep 300 & kill -STOP $!; echo job-$((40+2))\r"},
	} {
		t.Run(job.name, func(t *testing.T) {
			t.Parallel()
			_, r := runShellTUI(t, nil)
			id := r.openTerminalShell(t)
			r.keys(t, decltest.Type(job.line)...)
			r.s.WaitForText(t, "job-42")
			r.s.WaitFor(t, "the job behind the prompt read as running", func(string) bool {
				return r.shellActivity(id) == terminal.TerminalRunning
			})
			r.pressTabX(t, "shell")
			r.s.WaitForText(t, "close the shell?")
			r.keys(t, enter())
			r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "close the shell?") })
			if got := r.liveLabels(); !slices.Equal(got, []string{"shell"}) {
				t.Fatalf("No left tabs %v; want the shell's", got)
			}
			// the job ended, the shell is idle, and the × closes it at once
			r.s.WaitFor(t, "the keyboard back in the shell", func(string) bool { return r.focused("terminalView") })
			r.keys(t, decltest.Type("kill -9 $!; wait $! 2>/dev/null; echo done-$((40+2))\r")...)
			r.s.WaitForText(t, "done-42")
			r.s.WaitFor(t, "the shell idle", func(string) bool { return r.shellActivity(id) == terminal.TerminalIdle })
			r.pressTabX(t, "shell")
			r.s.WaitForText(t, "No shell running")
		})
	}
}
