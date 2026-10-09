//go:build linux || darwin

package tui

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
	p := prefs{termSize: map[string]int{}, termLength: map[string]int{}}
	readTermPrefs(&p, map[string]any{prefTermPrefix + "bottom" + prefTermLength: "60"})
	if size, length := p.termGeometry("bottom"); size != 30 || length != 60 {
		t.Errorf("bottom geometry = %d × %d, want 30 × 60", size, length)
	}
	if size, length := p.termGeometry("center"); size != 70 || length != 70 {
		t.Errorf("center geometry = %d × %d, want 70 × 70", size, length)
	}
}

func (r *running) terminal() *widget.Terminal {
	return onLoop(r, func() *widget.Terminal {
		c, _ := r.h.p.Find("terminalView")
		term, _ := c.(*widget.Terminal)
		return term
	})
}

func (r *running) termMode() widget.TerminalMode {
	return onLoop(r, func() widget.TerminalMode {
		c, _ := r.h.p.Find("terminalView")
		return c.(*widget.Terminal).Mode()
	})
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
	r.s.WaitFor(t, "Normal mode", func(string) bool { return r.termMode() == widget.TerminalNormal })
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
	if !r.terminal().Running() {
		t.Error("the shell stopped while hidden")
	}
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
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
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "back to the explorer", func(string) bool { return r.focused("explorerTree") })

	// Hidden while another pane has the keyboard, it moves nothing.
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("editor", "forceActiveFocus")); return true })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "hidden", func(string) bool { return !r.termShown() })
	time.Sleep(50 * time.Millisecond)
	if !r.focused("editor") {
		t.Error("hiding a terminal without the keyboard moved the keyboard")
	}
}

func TestTheTerminalsPlaceAndSizeFollowThePreferences(t *testing.T) {
	d, r := runShellTUI(t, map[string]string{"tui.terminal.bottom.size": "50"})
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	// At the bottom, 50% high, of the 30 rows: its frame at row 15, the full width.
	r.s.WaitFor(t, "at the bottom, half high", func(sc string) bool {
		row, c := frameAt(sc)
		return row == 15 && c == 0
	})
	onLoop(r, func() bool { r.h.setTerminalEdge(indexOf(termEdges, "center")); return true })
	// Centred, with the centre's own defaults (70 x 70), not the bottom's 50: 70 of 100 columns
	// from column 15, 21 of 30 rows from row 4.
	r.s.WaitFor(t, "centred at its own size", func(sc string) bool {
		row, c := frameAt(sc)
		return row == 4 && c == 15
	})
	onLoop(r, func() bool { r.h.setTerminalSize(indexOf(percentLabels(termSizes), "40%")); return true })
	r.s.WaitFor(t, "the centre's size changed", func(sc string) bool {
		row, _ := frameAt(sc)
		return row == 9 // 12 of 30 rows, centred
	})
	for _, edge := range []string{"top", "left", "right"} {
		onLoop(r, func() bool { r.h.setTerminalEdge(indexOf(termEdges, edge)); return true })
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

func TestTheTerminalsVimKeysFollowTheEditorsKeymap(t *testing.T) {
	_, r := runShellTUI(t, map[string]string{"tui.editor.keymap": "text"})
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, esc())
	time.Sleep(100 * time.Millisecond)
	if r.termMode() != widget.TerminalInput {
		t.Fatal("Esc left input mode with the Text keymap")
	}
	onLoop(r, func() bool { r.h.setKeymap("vim"); return true })
	r.keys(t, esc())
	r.s.WaitFor(t, "Normal mode with the Vim keymap", func(string) bool { return r.termMode() == widget.TerminalNormal })
}

func TestTheTerminalSaysWhenTheShellExitsAndStartsAnother(t *testing.T) {
	_, r := runShellTUI(t, nil)
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, decltest.Type("exit 4\r")...)
	r.s.WaitForText(t, "[process exited 4]")
	r.s.WaitFor(t, "the exit in the notifications", func(string) bool {
		return onLoop(r, func() bool {
			for _, n := range r.h.notices {
				if strings.Contains(n.text, "exited (4)") {
					return true
				}
			}
			return false
		})
	})
	r.keys(t, enter())
	r.keys(t, decltest.Type("echo again-$((40+2))\r")...)
	r.s.WaitForText(t, "again-42")
}

// A shell that forwards its hang-up to its jobs (bash and zsh do; dash, Ubuntu's /bin/sh, does not)
// ends with them when AutoDoc quits.
func TestQuittingHangsTheShellUpWithItsJobs(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash: the cell needs a shell that forwards its hang-up to its jobs")
	}
	_, r := runTUIWithShell(t, bash, nil)
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
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
	deadline := time.Now().Add(10 * time.Second)
	for syscall.Kill(job, 0) == nil {
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
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "the failure noticed", func(string) bool {
		return onLoop(r, func() bool {
			for _, n := range r.h.notices {
				if strings.Contains(n.text, "could not start") {
					return true
				}
			}
			return false
		})
	})
	if onLoop(r, func() bool { return r.h.termStarted }) {
		t.Fatal("a shell that failed to start counts as started")
	}
	// The command works now (as a fixed $SHELL would make it at the next run); the next open starts it.
	term := r.terminal() // outside onLoop: it runs on the loop itself
	onLoop(r, func() bool { term.SetCommand("/bin/sh"); return true })
	onLoop(r, func() bool { r.h.toggleTerminal(); return true }) // hide
	r.s.WaitFor(t, "hidden", func(string) bool { return !r.termShown() })
	onLoop(r, func() bool { r.h.toggleTerminal(); return true }) // open again
	r.s.WaitFor(t, "the shell started on the retry", func(string) bool {
		return onLoop(r, func() bool { return term.Running() })
	})
}
