//go:build linux || darwin

package tui

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// terminal_test.go: the terminal pane — SPC ` and Go › Terminal, a shell in the workspace's folder,
// the keyboard in and back out, its place and size from the preferences.

// runShellTUI runs the TUI with /bin/sh as the user's shell, so the cells do not depend on the
// machine's own.
func runShellTUI(t *testing.T, prefs map[string]string) (*daemon, *running) {
	t.Helper()
	return runTUIWithShell(t, "/bin/sh", prefs)
}

// runTUIWithShell is runShellTUI with shell as the user's shell.
func runTUIWithShell(t *testing.T, shell string, prefs map[string]string) (*daemon, *running) {
	t.Helper()
	t.Setenv("SHELL", shell)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PS1", "$ ")
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: prefs})
	return d, attached(t, d)
}

func TestTerminalStoredLengthAppliesOnlyToItsEdge(t *testing.T) {
	var p prefs
	readPanelPrefs(&p, map[string]any{"tui.terminal.bottom.length": "60"})
	if size, length := p.panelGeometry("terminal", "bottom"); size != 30 || length != 60 {
		t.Errorf("bottom geometry = %d × %d, want 30 × 60", size, length)
	}
	if size, length := p.panelGeometry("terminal", "center"); size != 70 || length != 70 {
		t.Errorf("center geometry = %d × %d, want 70 × 70", size, length)
	}
}

// Stored terminal geometry uses the same preference names as before the
// shared geometry reader. Read a pre-existing preferences snapshot, not a
// snapshot produced by the new setters.
func TestTerminalExistingGeometryPreferencesReadBack(t *testing.T) {
	p := prefsOf(map[string]any{
		"tui.terminal.edge":          "left",
		"tui.terminal.left.size":     "40",
		"tui.terminal.left.length":   "60",
		"tui.terminal.center.size":   "80",
		"tui.terminal.center.length": "70",
		"tui.agent.top.size":         "50",
		"tui.agent.top.length":       "90",
	})
	st := termState(p)
	if st["App.terminalEdge"] != "left" || st["App.terminalSize"] != 40 || st["App.terminalLength"] != 60 {
		t.Fatalf("existing terminal preferences read as %v", st)
	}
	if size, length := p.panelGeometry("terminal", "center"); size != 80 || length != 70 {
		t.Errorf("stored center geometry = %d × %d, want 80 × 70", size, length)
	}
	if size, length := p.panelGeometry("terminal", "bottom"); size != 30 || length != 100 {
		t.Errorf("unset bottom geometry = %d × %d, want defaults 30 × 100", size, length)
	}
	if size, length := p.panelGeometry("agent", "top"); size != 50 || length != 90 {
		t.Errorf("agent geometry = %d × %d, want 50 × 90", size, length)
	}
}

// shellTabs are the terminal panel's tabs, a snapshot taken on the loop.
func (r *running) shellTabs() []widget.TerminalTab {
	return onLoop(r, func() []widget.TerminalTab {
		c, _ := r.h.p.Find("terminalView")
		tabs, ok := tuidecl.TerminalTabsOf(c)
		if !ok {
			return nil
		}
		return tabs.Tabs()
	})
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

func TestTheTerminalTogglesFromTheLeaderAndGivesTheKeyboardBack(t *testing.T) {
	_, r := runShellTUI(t, nil)
	// The tests' workspaces live in memory, not on this disk: the shell starts in the home folder.
	root := os.Getenv("HOME")

	r.leader(t, '`')
	r.s.WaitForText(t, "┌ terminal")
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, decltest.Type("pwd\r")...)
	r.s.WaitFor(t, "the shell in the workspace's folder", func(sc string) bool {
		return strings.Contains(sc, root[max(0, len(root)-30):])
	})

	// Esc in input mode (Vim keys, the default keymap) leaves for Normal mode; the pane stays.
	r.keys(t, esc())
	time.Sleep(150 * time.Millisecond)
	if !r.termShown() {
		t.Fatal("Esc in input mode closed the pane")
	}
	// Esc in Normal mode, with nothing to cancel, hides it; the page has the keyboard again.
	r.keys(t, esc())
	r.s.WaitFor(t, "the pane hidden", func(sc string) bool { return !strings.Contains(sc, "┌ terminal") })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })

	// Hidden, the shell lives on: opening it again shows what it showed.
	r.leader(t, '`')
	r.s.WaitForText(t, "┌ terminal")
	r.s.WaitFor(t, "the same shell", func(sc string) bool { return strings.Contains(sc, root[max(0, len(root)-30):]) })
	if r.shellsRunning() != 1 {
		t.Errorf("the shell stopped while hidden: %+v", r.shellTabs())
	}
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "hidden by its key", func(sc string) bool { return !strings.Contains(sc, "┌ terminal") })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })

	// The card lists it.
	r.keys(t, key(' '))
	r.s.WaitForText(t, "`  the terminal")
	r.keys(t, esc())
}

func TestTheTerminalGivesTheKeyboardBackToThePaneThatHadIt(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, 'e')
	r.s.WaitFor(t, "the explorer's keyboard", func(string) bool { return r.focused("explorerTree") })
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "back to the explorer", func(string) bool { return r.focused("explorerTree") })

	// Hidden while another pane has the keyboard, it moves nothing.
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("editor", "forceActiveFocus")); return true })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "hidden", func(string) bool { return !r.termShown() })
	time.Sleep(50 * time.Millisecond)
	if !r.focused("editor") {
		t.Error("hiding a terminal without the keyboard moved the keyboard")
	}
}

func TestTheTerminalsPlaceAndSizeFollowThePreferences(t *testing.T) {
	d, r := runShellTUI(t, map[string]string{"tui.terminal.bottom.size": "50"})
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	// At the bottom, 50% high, of the 30 rows: its frame at row 15, the full width.
	r.s.WaitFor(t, "at the bottom, half high", func(sc string) bool {
		row, c := frameAt(sc)
		return row == 15 && c == 0
	})
	onLoop(r, func() bool { r.h.terminals.setTerminalEdge(indexOf(termEdges, "center")); return true })
	// Centred, with the centre's own defaults (70 x 70), not the bottom's 50: 70 of 100 columns
	// from column 15, 21 of 30 rows from row 4.
	r.s.WaitFor(t, "centred at its own size", func(sc string) bool {
		row, c := frameAt(sc)
		return row == 4 && c == 15
	})
	onLoop(r, func() bool { r.h.terminals.setTerminalSize(indexOf(percentLabels(termSizes), "40%")); return true })
	r.s.WaitFor(t, "the centre's size changed", func(sc string) bool {
		row, _ := frameAt(sc)
		return row == 9 // 12 of 30 rows, centred
	})
	for _, edge := range []string{"top", "left", "right"} {
		onLoop(r, func() bool { r.h.terminals.setTerminalEdge(indexOf(termEdges, edge)); return true })
		r.s.WaitFor(t, "at the "+edge, func(sc string) bool {
			row, c := frameAt(sc)
			switch edge {
			case "top":
				return row == 0 && c == 0
			case "left":
				return row == 0 && c == 0 && strings.Contains(strings.Split(sc, "\n")[0], "┐") &&
					col(strings.Split(sc, "\n")[0], "┐") < 50
			}
			return row == 0 && c == 60 // 40% of 100 columns, at the right
		})
	}
	// Kept in the daemon's store, so a restart finds them. The screen follows a change at once and
	// its write lands after: wait for the values written, not any value.
	r.s.WaitFor(t, "edge right, center.size 40, bottom.size 50 stored", func(string) bool {
		got, err := d.db.Preferences(context.Background())
		return err == nil && got["tui.terminal.edge"] == "right" && got["tui.terminal.center.size"] == "40" &&
			got["tui.terminal.bottom.size"] == "50"
	})
	again := attached(t, d)
	if e := onLoop(again, func() string { return again.h.prefs.termEdge }); e != "right" {
		t.Errorf("a new TUI opens the terminal from %q, want the kept right", e)
	}
}

// The keymap reaches a shell already running: with the Text keymap Esc is the shell's, and the
// panel stays; with the Vim keymap the first Esc is Normal mode and the second hides the panel.
func TestTheTerminalsVimKeysFollowTheEditorsKeymap(t *testing.T) {
	_, r := runShellTUI(t, map[string]string{"tui.editor.keymap": "text"})
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, esc(), esc())
	r.keys(t, decltest.Type("\recho still-$((40+2))\r")...) // the shell had both Escs (a line of its own), and answers
	r.s.WaitForText(t, "still-42")
	if !r.termShown() || !r.focused("terminalView") {
		t.Fatal("Esc left the shell with the Text keymap")
	}
	onLoop(r, func() bool { r.h.setKeymap("vim"); return true })
	r.keys(t, esc(), esc())
	r.s.WaitFor(t, "Normal mode, then hidden, with the Vim keymap", func(string) bool { return !r.termShown() })
}

// TestTheTerminalClosesWhenItsShellExits: exit (or Ctrl+D) closes the panel as SPC ` would, the
// keyboard back on the page; exit 0 says nothing, a non-zero exit keeps a notice; SPC ` then starts a
// fresh shell.
func TestTheTerminalClosesWhenItsShellExits(t *testing.T) {
	_, r := runShellTUI(t, nil)
	exitNoticed := func(code string) bool {
		return onLoop(r, func() bool {
			for _, n := range r.h.noticeLog.items {
				if strings.Contains(n.text, "shell exited ("+code+")") {
					return true
				}
			}
			return false
		})
	}
	open := func() {
		t.Helper()
		r.leader(t, '`')
		r.s.WaitForText(t, "┌ terminal")
		r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	}
	closed := func(why string) {
		t.Helper()
		r.s.WaitFor(t, why, func(sc string) bool { return !strings.Contains(sc, "┌ terminal") })
		r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	}

	open()
	r.keys(t, decltest.Type("exit\r")...)
	closed("closed by exit")
	if exitNoticed("0") {
		t.Error("exit 0 left a notice")
	}

	open() // a fresh shell: it answers
	r.keys(t, decltest.Type("echo again-$((40+2))\r")...)
	r.s.WaitForText(t, "again-42")
	r.keys(t, decltest.Type("exit 4\r")...)
	closed("closed by exit 4")
	r.s.WaitFor(t, "the exit in the notifications", func(string) bool { return exitNoticed("4") })

	open() // and fresh again after a failing exit
	r.keys(t, decltest.Type("echo third-$((40+3))\r")...)
	r.s.WaitForText(t, "third-43")
}

// A shell that forwards its hang-up to its jobs (bash and zsh do; dash, Ubuntu's /bin/sh, does not)
// ends with them when AutoDoc quits.
func TestQuittingHangsTheShellUpWithItsJobs(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash: the cell needs a shell that forwards its hang-up to its jobs")
	}
	_, r := runTUIWithShell(t, bash, nil)
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	// +H: no history expansion of the $! below (macOS's bash 3.2 applies it).
	r.keys(t, decltest.Type("set +H; sleep 1000 & echo J''OB=$!\r")...)
	var job int
	re := regexp.MustCompile(`JOB=(\d+)`)
	r.s.WaitFor(t, "the job's pid", func(sc string) bool {
		m := re.FindStringSubmatch(sc)
		if m == nil {
			return false
		}
		job, _ = strconv.Atoi(m[1])
		return true
	})
	onLoop(r, func() bool { r.h.p.Quit(); return true })
	// Ended is gone or a zombie. The shell exits on the hang-up, so its job, ended, is reaped by
	// whoever adopts it: in a container whose PID 1 is go test itself, nobody, and kill(pid, 0)
	// still finds it.
	deadline := time.Now().Add(10 * time.Second)
	for pidAlive(job) && !zombie(job) {
		if time.Now().After(deadline) {
			syscall.Kill(job, syscall.SIGKILL)
			t.Fatalf("the shell's job %d outlived quitting", job)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTheShellStartsInTheWorkspacesFolderWhenThisMachineHasIt(t *testing.T) {
	dir := t.TempDir()
	if got := termDir(dir); got != dir {
		t.Errorf("termDir(%q) = %q, want the folder itself", dir, got)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := termDir("/no/such/workspace"); got != home {
		t.Errorf("termDir of a missing root = %q, want the home %q", got, home)
	}
	file := dir + "/a.md"
	os.WriteFile(file, []byte("x"), 0o644)
	if got := termDir(file); got != home {
		t.Errorf("termDir of a file = %q, want the home %q", got, home)
	}
}

// A shell that fails to start says so, and the terminal tries again: on the next open, once the
// command works.
func TestTheTerminalRetriesAShellThatFailedToStart(t *testing.T) {
	_, r := runTUIWithShell(t, "/nonexistent/shell", nil)
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the failure noticed", func(string) bool {
		return onLoop(r, func() bool {
			for _, n := range r.h.noticeLog.items {
				if strings.Contains(n.text, "could not start") {
					return true
				}
			}
			return false
		})
	})
	if tabs := r.shellTabs(); len(tabs) != 1 || tabs[0].Running || tabs[0].StartError == nil {
		t.Fatalf("a shell that failed to start: tabs %+v", tabs)
	}
	// $SHELL is fixed; the next open drops the failed tab and starts a shell with it
	t.Setenv("SHELL", "/bin/sh")
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true }) // hide
	r.s.WaitFor(t, "hidden", func(string) bool { return !r.termShown() })
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true }) // open again
	r.s.WaitFor(t, "the shell started on the retry", func(string) bool {
		tabs := r.shellTabs()
		return len(tabs) == 1 && tabs[0].Running
	})
}

// :terminal opens the terminal with the keyboard in it; again, with it open, it keeps it open.
func TestTheTerminalCommandOpensTheTerminal(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.command(t, "terminal")
	r.s.WaitForText(t, "┌ terminal")
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, esc(), esc()) // Normal mode, then hidden
	r.s.WaitFor(t, "the pane hidden", func(sc string) bool { return !strings.Contains(sc, "┌ terminal") })
	r.command(t, "term")
	r.s.WaitForText(t, "┌ terminal")
	// open already: the keyboard back in the page, :terminal gives it to the terminal again and
	// leaves the pane as it is
	onLoop(r, func() error { return r.h.p.Call("editor", "forceActiveFocus") })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	if !r.termShown() {
		t.Fatal("the terminal closed when the page took the keyboard")
	}
	r.command(t, "terminal")
	r.s.WaitFor(t, "the terminal's keyboard again", func(string) bool { return r.focused("terminalView") })
	if !r.termShown() {
		t.Fatal(":terminal over an open terminal closed it")
	}
}

// TestTheTerminalsShellsAreTabs: New on a tab's menu (as +) adds a shell in a tab of its own, named
// shell 2; an exit closes only its tab, the keyboard going to the next shell; Go to › selects a
// tab; Close on the last leaves the panel open on its empty text, and SPC ` then starts a fresh one.
func TestTheTerminalsShellsAreTabs(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitForText(t, "┌ terminal")
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	menu := func(id string) {
		t.Helper()
		rows, tab := onLoop(r, func() []widget.MenuItemModel {
			c, _ := r.h.p.Find("terminalView")
			tabs, _ := tuidecl.TerminalTabsOf(c)
			return r.h.terminals.tabRows(tabs.Active())
		}), onLoop(r, func() widget.TerminalTabID {
			c, _ := r.h.p.Find("terminalView")
			tabs, _ := tuidecl.TerminalTabsOf(c)
			return tabs.Active()
		})
		row, ok := menuRow(rows, id)
		if !ok {
			t.Fatalf("no row %s in %v", id, rowIDs(rows))
		}
		onLoop(r, func() bool { row.Action.(widget.TerminalTabsMenuAction).Run(tab); return true })
	}
	labels := func() []string {
		var out []string
		for _, tab := range r.shellTabs() {
			if !tab.Closing {
				out = append(out, tab.Label)
			}
		}
		return out
	}

	menu("terminal.new")
	r.s.WaitFor(t, "two shells running", func(string) bool { return r.shellsRunning() == 2 })
	if got := labels(); !slices.Equal(got, []string{"shell", "shell 2"}) {
		t.Fatalf("tabs %v", got)
	}
	r.s.WaitFor(t, "the new shell's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, decltest.Type("echo two-$((40+2))\r")...)
	r.s.WaitForText(t, "two-42")

	// its exit closes its tab only: the panel stays, on the first shell, which answers
	r.keys(t, decltest.Type("exit\r")...)
	r.s.WaitFor(t, "one shell left", func(string) bool { return slices.Equal(labels(), []string{"shell"}) })
	if !r.termShown() {
		t.Fatal("a shell's exit closed the panel with another shell running")
	}
	r.s.WaitFor(t, "the first shell's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, decltest.Type("echo one-$((40+1))\r")...)
	r.s.WaitForText(t, "one-41")

	// Go to › selects a tab
	menu("terminal.new")
	r.s.WaitFor(t, "two shells again", func(string) bool { return r.shellsRunning() == 2 })
	first := r.shellTabs()[0].ID
	menu("tabs.goto." + string(first))
	r.s.WaitFor(t, "the first selected", func(string) bool {
		return onLoop(r, func() bool {
			c, _ := r.h.p.Find("terminalView")
			tabs, _ := tuidecl.TerminalTabsOf(c)
			return tabs.Active() == first
		})
	})

	// Close on each: the last leaves the panel open on its empty text
	menu("terminal.close")
	r.s.WaitFor(t, "one shell after Close", func(string) bool { return len(labels()) == 1 })
	menu("terminal.close")
	r.s.WaitForText(t, "No shell running")
	if !r.termShown() {
		t.Fatal("Close on the last tab closed the panel")
	}
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "hidden", func(string) bool { return !r.termShown() })
	r.leader(t, '`')
	r.s.WaitFor(t, "a fresh shell", func(string) bool { return r.shellsRunning() == 1 })
}

// TestABackgroundShellLeavingKeepsTheActiveOne: with three shells and the last active, a background
// shell's exit, and Close on a background tab's menu, leave the active shell selected with the
// keyboard; only the active tab's going moves it.
func TestABackgroundShellLeavingKeepsTheActiveOne(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	tabsOf := func(fn func(*widget.TerminalTabs)) {
		onLoop(r, func() bool {
			c, _ := r.h.p.Find("terminalView")
			tabs, _ := tuidecl.TerminalTabsOf(c)
			fn(tabs)
			return true
		})
	}
	active := func() (id widget.TerminalTabID) {
		tabsOf(func(tabs *widget.TerminalTabs) { id = tabs.Active() })
		return id
	}
	live := func() []widget.TerminalTabID {
		var out []widget.TerminalTabID
		for _, tab := range r.shellTabs() {
			if !tab.Closing {
				out = append(out, tab.ID)
			}
		}
		return out
	}
	onLoop(r, func() bool { r.h.terminals.newShell(); r.h.terminals.newShell(); return true })
	r.s.WaitFor(t, "three shells", func(string) bool { return r.shellsRunning() == 3 })
	ids := live()
	a, b, c := ids[0], ids[1], ids[2]
	if active() != c {
		t.Fatalf("the newest shell is not active: %s", active())
	}

	// a background shell exits
	tabsOf(func(tabs *widget.TerminalTabs) {
		if err := tabs.SendText(b, "exit\r"); err != nil {
			t.Error(err)
		}
	})
	r.s.WaitFor(t, "the background shell gone", func(string) bool { return slices.Equal(live(), []widget.TerminalTabID{a, c}) })
	if got := active(); got != c {
		t.Fatalf("a background exit selected %s, want the active %s", got, c)
	}
	r.s.WaitFor(t, "the active shell's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, decltest.Type("echo kept-$((40+3))\r")...)
	r.s.WaitForText(t, "kept-43")

	// Close on a background tab's menu
	rows := onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows(a) })
	row, ok := menuRow(rows, "terminal.close")
	if !ok {
		t.Fatalf("no Close in %v", rowIDs(rows))
	}
	onLoop(r, func() bool { row.Action.(widget.TerminalTabsMenuAction).Run(a); return true })
	r.s.WaitFor(t, "the background tab closed", func(string) bool { return slices.Equal(live(), []widget.TerminalTabID{c}) })
	if got := active(); got != c {
		t.Fatalf("a background Close selected %s, want the active %s", got, c)
	}
}

// TestTheTerminalsThemeIsTheirsAlone: the terminals' theme is the shell's background and default
// colours, the panel's strip staying the page's; a program's colour is raised against the theme's
// background, not the page's; "the page's" follows the page, a theme switch included.
func TestTheTerminalsThemeIsTheirsAlone(t *testing.T) {
	_, r := runShellTUI(t, map[string]string{"tui.terminal.theme": "light"}) // on the dark page
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	// a pale grey: unreadable on light's background, plain on dark's; typed split, so only the
	// output says PALE
	r.keys(t, decltest.Type("printf '\\033[38;2;240;240;240mPA''LE\\033[0m\\n'\r")...)
	r.s.WaitForText(t, "PALE")
	pale := func() (fg, bg tuicore.CellColor) {
		for _, row := range r.s.Backend.Snapshot() {
			for x := 0; x+3 < len(row); x++ {
				if row[x].Content == "P" && row[x+1].Content == "A" && row[x+2].Content == "L" && row[x+3].Content == "E" {
					return row[x].Attrs.FG, row[x].Attrs.BG
				}
			}
		}
		t.Fatalf("no PALE on screen:\n%s", r.s.String())
		return
	}
	rgb := func(r, g, b uint8) tuicore.CellColor {
		return tuicore.CellColor{Kind: tuicore.CellColorRGB, R: r, G: g, B: b}
	}
	light, dark, grey := rgb(0xf6, 0xf2, 0xe7), rgb(0x1c, 0x1c, 0x1c), rgb(240, 240, 240)
	if fg, bg := pale(); bg != light || fg == grey {
		t.Fatalf("light terminals on a dark page: PALE in %+v on %+v; want light's background, the grey raised", fg, bg)
	}
	strip := func() tuicore.CellColor {
		for _, row := range r.s.Backend.Snapshot() {
			for x := 0; x+4 < len(row); x++ {
				if row[x].Content == "s" && row[x+1].Content == "h" && row[x+2].Content == "e" && row[x+3].Content == "l" && row[x+4].Content == "l" {
					return row[x].Attrs.BG
				}
			}
		}
		t.Fatalf("no tab strip on screen:\n%s", r.s.String())
		return tuicore.CellColor{}
	}
	if bg := strip(); bg == light {
		t.Errorf("the strip took the terminals' theme: %+v", bg)
	}

	onLoop(r, func() bool { r.h.terminals.setTerminalTheme(0); return true }) // the page's: dark
	r.s.WaitFor(t, "the page's dark background", func(string) bool { _, bg := pale(); return bg == dark })
	if fg, _ := pale(); fg != grey {
		t.Errorf("the grey on dark was changed: %+v", fg)
	}
	onLoop(r, func() bool { r.h.switchTheme("light"); return true })
	r.s.WaitFor(t, "the page's, after the switch: light", func(string) bool { _, bg := pale(); return bg == light })
}

// The Preferences terminal row's tooltip says the theme is the shells' and leaves a program's own
// colours alone: the pointer resting on the row's description shows it.
func TestTheTerminalThemeRowSaysWhatItColours(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, ',')
	r.s.WaitForText(t, "the terminal: from")
	x, y := -1, -1
	for row, line := range strings.Split(r.s.String(), "\n") {
		if c := col(line, "the terminal: from"); c >= 0 {
			x, y = c+2, row
		}
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseMotion, X: x, Y: y})
	r.s.WaitForText(t, "16 colours")
}

// TestHideClosesThePanelAndKeepsTheShells: Hide on a shell tab's menu closes the terminal panel,
// its shells running on, the same tabs; opening it again shows them, the same one active, and no
// new shell. An empty panel's menu has Hide and Theme and font…, and no Close.
func TestHideClosesThePanelAndKeepsTheShells(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	onLoop(r, func() bool { r.h.terminals.newShell(); return true })
	r.s.WaitFor(t, "two shells", func(string) bool { return r.shellsRunning() == 2 })
	active := func() (id widget.TerminalTabID) {
		return onLoop(r, func() widget.TerminalTabID {
			c, _ := r.h.p.Find("terminalView")
			tabs, _ := tuidecl.TerminalTabsOf(c)
			return tabs.Active()
		})
	}
	before, was := r.shellTabs(), active()
	rows := onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows(was) })
	row, ok := menuRow(rows, "tabs.hide")
	if !ok || row.Label != "Hide" || row.Hotkey != 'd' {
		t.Fatalf("no Hide (d) in %v", rowIDs(rows))
	}
	if _, ok := menuRow(rows, "tabs.look"); !ok {
		t.Fatalf("no Theme and font… in %v", rowIDs(rows))
	}
	onLoop(r, func() bool { row.Action.(widget.TerminalTabsMenuAction).Run(was); return true })
	r.s.WaitFor(t, "hidden", func(string) bool { return !r.termShown() })
	after := r.shellTabs()
	if len(after) != 2 || r.shellsRunning() != 2 || after[0].ID != before[0].ID || after[0].Generation != before[0].Generation || after[1].Generation != before[1].Generation {
		t.Fatalf("Hide changed the shells: %+v, before %+v", after, before)
	}
	r.leader(t, '`')
	r.s.WaitFor(t, "shown again", func(string) bool { return r.termShown() })
	if got := active(); got != was || r.shellsRunning() != 2 || len(r.shellTabs()) != 2 {
		t.Fatalf("reopened on %s (want %s), %d running of %d", got, was, r.shellsRunning(), len(r.shellTabs()))
	}
	empty := onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows("") })
	if row, _ := menuRow(empty, "terminal.close"); row.Enabled {
		t.Error("an empty panel's menu offers Close")
	}
	for _, id := range []string{"tabs.hide", "tabs.look", "terminal.new"} {
		if _, ok := menuRow(empty, id); !ok {
			t.Errorf("an empty panel's menu lacks %s", id)
		}
	}
}

// TestTheTerminalsLookDialogSetsTheSameSettings: Theme and font… from a panel's menu opens a dialog
// over the terminals' theme; a choice shows at once and is the one Preferences keeps; Esc puts back
// what was chosen before, OK keeps the choice.
func TestTheTerminalsLookDialogSetsTheSameSettings(t *testing.T) {
	d, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	look := func() {
		t.Helper()
		rows := onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows("") })
		row, ok := menuRow(rows, "tabs.look")
		if !ok {
			t.Fatalf("no Theme and font… in %v", rowIDs(rows))
		}
		onLoop(r, func() bool { row.Action.(widget.TerminalTabsMenuAction).Run(""); return true })
		r.s.WaitForText(t, "the terminals' theme and font")
	}
	light := slices.Index(termThemes(), "light")
	look()
	r.s.WaitForText(t, "a terminal draws in its own") // the terminal UI: no font row
	onLoop(r, func() bool { r.h.terminals.setTerminalTheme(light); return true })
	storedPref(t, d, r, prefTermTheme, "light")
	r.keys(t, esc())
	storedPref(t, d, r, prefTermTheme, "page") // Esc: what was chosen before
	look()
	onLoop(r, func() bool { r.h.terminals.setTerminalTheme(light); return true })
	storedPref(t, d, r, prefTermTheme, "light")
	onLoop(r, func() bool { r.h.terminalLookKept(); r.h.closeDialog("terminalLook"); return true })
	if got := onLoop(r, func() string { return r.h.prefs.termTheme }); got != "light" {
		t.Errorf("OK kept %q", got)
	}
}

// TestMaximizeFromThePanelMenus: Maximize on the terminal's tab menu fills the dock area (its
// Drawer's own toggleMaximize), the row then reads Restore, and Restore puts the frame back; Hide
// while maximized keeps the shells. The agents' menu maximizes the agent panel the same way.
func TestMaximizeFromThePanelMenus(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	row0, _ := frameAt(r.s.String())
	maximize := func(want string) {
		t.Helper()
		rows := onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows("") })
		row, ok := menuRow(rows, "tabs.maximize")
		if !ok || row.Label != want {
			t.Fatalf("the maximize row reads %q (want %q) in %v", row.Label, want, rowIDs(rows))
		}
		onLoop(r, func() bool { row.Action.(widget.TerminalTabsMenuAction).Run(""); return true })
	}
	maximize("Maximize")
	r.s.WaitFor(t, "the terminal fills the dock area", func(sc string) bool { y, _ := frameAt(sc); return y >= 0 && y < row0 })
	r.s.WaitFor(t, "maximized told", func(string) bool { return onLoop(r, func() bool { return r.h.panelMax["terminal"] }) })
	maximize("Restore")
	r.s.WaitFor(t, "the frame back", func(sc string) bool { y, _ := frameAt(sc); return y == row0 })

	maximize("Maximize")
	r.s.WaitFor(t, "maximized again", func(sc string) bool { y, _ := frameAt(sc); return y >= 0 && y < row0 })
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true }) // Hide
	r.s.WaitFor(t, "hidden", func(string) bool { return !r.termShown() })
	if r.shellsRunning() != 1 {
		t.Fatalf("Hide while maximized stopped the shell: %+v", r.shellTabs())
	}
	r.leader(t, '`')
	r.s.WaitFor(t, "shown again", func(string) bool { return r.termShown() })
	if r.shellsRunning() != 1 || len(r.shellTabs()) != 1 {
		t.Fatalf("reopened with %+v", r.shellTabs())
	}

	agentRows := onLoop(r, func() []widget.MenuItemModel { return r.h.agents.tabRows("") })
	if row, ok := menuRow(agentRows, "tabs.maximize"); !ok || row.Label != "Maximize" {
		t.Fatalf("the agents' menu: %v", rowIDs(agentRows))
	}
}

// TestF11AndSPCMToggleMaximize: F11 maximizes the terminal panel and restores it, from the page (the
// terminal opened last); SPC M does the same; the tab menu's row follows, Maximize and Restore.
func TestF11AndSPCMToggleMaximize(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	maximized := func() bool { return onLoop(r, func() bool { return r.h.panelMax["terminal"] }) }
	label := func() string {
		row, _ := menuRow(onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows("") }), "tabs.maximize")
		return row.Label
	}
	f11 := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF11}
	onLoop(r, func() bool { return r.h.p.Call("editor", "forceActiveFocus") == nil })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	r.keys(t, f11)
	r.s.WaitFor(t, "F11 maximized it", func(string) bool { return maximized() })
	if got := label(); got != "Restore" {
		t.Errorf("maximized, the menu reads %q", got)
	}
	r.keys(t, f11)
	r.s.WaitFor(t, "F11 restored it", func(string) bool { return !maximized() })

	r.leader(t, 'M')
	r.s.WaitFor(t, "SPC M maximized it from the page", func(string) bool { return maximized() })
	if got := label(); got != "Restore" {
		t.Errorf("maximized by SPC M, the menu reads %q", got)
	}
	r.leader(t, 'M')
	r.s.WaitFor(t, "SPC M restored it", func(string) bool { return !maximized() })
}

// TestSPCMReachesTheNewestPanelStillOpen: the terminal opened, then the agents opened and hidden,
// the keyboard on the page: SPC M maximizes the terminal, the newest of them still open.
func TestSPCMReachesTheNewestPanelStillOpen(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	onLoop(r, func() bool { r.h.openDrawer("agent"); return true })
	r.s.WaitFor(t, "the agents open", func(string) bool { return onLoop(r, func() bool { return r.h.panelOpen["agent"] }) })
	onLoop(r, func() bool { r.h.closeDrawer("agent"); return true })
	r.s.WaitFor(t, "the agents hidden", func(string) bool { return !onLoop(r, func() bool { return r.h.panelOpen["agent"] }) })
	onLoop(r, func() bool { return r.h.p.Call("editor", "forceActiveFocus") == nil })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	r.leader(t, 'M')
	r.s.WaitFor(t, "the terminal maximized", func(string) bool { return onLoop(r, func() bool { return r.h.panelMax["terminal"] }) })
}

// TestSPCMWithNoPanelOpenSaysWhatItMaximizes: neither the terminal nor the agents open, SPC M
// maximizes nothing and says which panels it is for.
func TestSPCMWithNoPanelOpenSaysWhatItMaximizes(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.leader(t, 'M')
	r.s.WaitFor(t, "the notice", func(string) bool {
		return r.noticed("Maximize is the terminal's and the agents': open one first (SPC ` or SPC g)")
	})
	if onLoop(r, func() bool { return r.h.panelMax["terminal"] || r.h.panelMax["agent"] }) {
		t.Error("a closed panel maximized")
	}
}

// TestMaximizePanelPrefersThePanelWithTheKeyboard: the agents opened, then the terminal, the
// keyboard back in the agents: maximizePanel (what F11 and SPC M run; TestF11AndSPCMToggleMaximize
// presses the keys) maximizes the agents, the panel with the keyboard, not the terminal opened
// last. A Drawer's change that is not a maximize leaves Restore as it was.
func TestMaximizePanelPrefersThePanelWithTheKeyboard(t *testing.T) {
	_, r := runShellTUI(t, nil)
	onLoop(r, func() bool { r.h.openDrawer("agent"); return true })
	r.s.WaitFor(t, "the agents open", func(string) bool { return onLoop(r, func() bool { return r.h.panelOpen["agent"] }) })
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	onLoop(r, func() bool { return r.h.p.Call("agentView", "forceActiveFocus") == nil })
	r.s.WaitFor(t, "the agents' keyboard", func(string) bool { return r.focused("agentView") })
	onLoop(r, func() bool { r.h.maximizePanel(); return true })
	r.s.WaitFor(t, "the agents maximized", func(string) bool { return onLoop(r, func() bool { return r.h.panelMax["agent"] }) })
	if onLoop(r, func() bool { return r.h.panelMax["terminal"] }) {
		t.Error("the terminal, opened last, maximized instead")
	}
	onLoop(r, func() bool { r.h.panelChanged("agent", "move", false); return true })
	if !onLoop(r, func() bool { return r.h.panelMax["agent"] }) {
		t.Error("a move restored the agents' Maximize row")
	}
}

// TestF11WorksWithTheKeyboardInTheShell: with the keyboard in a running shell, F11 maximizes the
// terminal panel and restores it (the panels' hostKeys leave it to the Window), and the shell
// never gets it: it answers as before.
func TestF11WorksWithTheKeyboardInTheShell(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	maximized := func() bool { return onLoop(r, func() bool { return r.h.panelMax["terminal"] }) }
	f11 := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF11}
	r.keys(t, f11)
	r.s.WaitFor(t, "F11 in the shell maximized it", maximizedNow(maximized, true))
	r.keys(t, f11)
	r.s.WaitFor(t, "F11 in the shell restored it", maximizedNow(maximized, false))
	if !r.focused("terminalView") {
		t.Error("F11 took the keyboard out of the shell")
	}
	r.keys(t, decltest.Type("echo f11-never-sent-$((40+6))\r")...)
	r.s.WaitForText(t, "f11-never-sent-46")
}

func maximizedNow(maximized func() bool, want bool) func(string) bool {
	return func(string) bool { return maximized() == want }
}

// clickMenuRow opens the open panel's ≡ menu with the pointer and clicks its row label.
func clickMenuRow(t *testing.T, r *running, label string) {
	t.Helper()
	click := func(x, y int) {
		r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
			tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	}
	r.s.WaitForText(t, "≡")
	click(r.textAt("≡"))
	r.s.WaitForText(t, label)
	x, y := r.textAt(label)
	click(x+1, y)
}

// TestAClickOnMaximizeInTheTerminalsMenu: a click on Maximize in the terminal's ≡ menu maximizes
// the panel, and a click on Restore restores it; nothing is refused. (rc2: the click was refused,
// the row running while its menu still held the input.)
func TestAClickOnMaximizeInTheTerminalsMenu(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	clickMaximizeAndRestore(t, r, "terminal")
}

// clickMaximizeAndRestore clicks Maximize, then Restore, in the open panel's ≡ menu: the panel
// maximizes, then restores, and nothing is kept as refused.
func clickMaximizeAndRestore(t *testing.T, r *running, panel string) {
	t.Helper()
	maxed := func(want bool) func(string) bool {
		return func(string) bool { return onLoop(r, func() bool { return r.h.panelMax[panel] }) == want }
	}
	// where ≡ is drawn: the maximized panel's strip is drawn elsewhere, and the next click must find
	// it there, not where the frame before showed it
	strip := func() [2]int { x, y := r.textAt("≡"); return [2]int{x, y} }
	r.s.WaitForText(t, "≡")
	docked := strip()
	clickMenuRow(t, r, "Maximize")
	r.s.WaitFor(t, "the "+panel+" maximized by a click", maxed(true))
	r.s.WaitFor(t, "the maximized "+panel+" drawn", func(string) bool { return strip() != docked })
	clickMenuRow(t, r, "Restore")
	r.s.WaitFor(t, "the "+panel+" restored by a click", maxed(false))
	r.s.WaitFor(t, "the restored "+panel+" drawn", func(string) bool { return strip() == docked })
	if errs := onLoop(r, func() []error { r.h.mu.Lock(); defer r.h.mu.Unlock(); return append([]error(nil), r.h.errs...) }); len(errs) != 0 {
		t.Errorf("refused: %v", errs)
	}
}
