//go:build linux || darwin

package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// hintProbe is the start of the normal-mode hint as the strip draws it (terminalkeymap.go): the mode
// key's name, which a narrow strip keeps when it cuts the rest.
const hintProbe = "Ctrl+T or i"

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

// catInTheTerminal opens the terminal panel on a shell with a page of output, then runs cat -v, which
// prints every line it reads: a key typed reaches the program when its line comes back from cat.
func catInTheTerminal(t *testing.T, r *running) {
	t.Helper()
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, decltest.Type("seq 1 40; printf 'cat-%s\\n' ready; cat -v\r")...)
	r.s.WaitForText(t, "cat-ready")
}

// wheelUp scrolls the terminal back where cat's banner shows.
func wheelUp(t *testing.T, r *running) {
	t.Helper()
	x, y := cellOf(t, r.s.String(), "cat-ready")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelUp, X: x, Y: y})
}

// cat printed line: the line typed came back from cat (once echoed, once printed).
func catPrinted(line string) func(string) bool {
	return func(sc string) bool { return strings.Count(sc, line) >= 2 }
}

// Under the text editor's keys the wheel's scrollback view is no mode to leave: the strip says how to
// get back while it shows, and the next key goes to the program with no i, the hint gone.
func TestATextUsersTerminalScrollsBackWithoutAMode(t *testing.T) {
	_, r := runShellTUI(t, map[string]string{prefKeymap: "text"})
	catInTheTerminal(t, r)
	wheelUp(t, r)
	r.s.WaitForText(t, hintProbe)
	r.keys(t, decltest.Type("typed-after\r")...)
	r.s.WaitFor(t, "the first key after the wheel reached cat", catPrinted("typed-after"))
	r.s.WaitFor(t, "the hint gone", func(sc string) bool { return !strings.Contains(sc, hintProbe) })
}

// Under Vim's keys the wheel is normal mode, as it always was: a key there is normal mode's and never
// the program's, i returns to input, and no hint shows.
func TestAVimUsersTerminalWheelIsNormalModeAsBefore(t *testing.T) {
	_, r := runShellTUI(t, nil) // the Vim keymap, the default
	catInTheTerminal(t, r)
	wheelUp(t, r)
	time.Sleep(150 * time.Millisecond) // the wheel has no sign of its own under Vim's keys
	r.keys(t, key('x'), key('i'))
	r.keys(t, decltest.Type("typed-after\r")...)
	r.s.WaitFor(t, "i, then the line, reached cat", catPrinted("typed-after"))
	sc := r.s.String()
	if strings.Contains(sc, "xtyped-after") || strings.Contains(sc, hintProbe) {
		t.Fatalf("normal mode's x reached cat, or the hint showed under Vim's keys:\n%s", sc)
	}
}

// Normal mode asked for stays modal under the text editor's keys, and the strip says how to leave
// it: by the mode key (Ctrl+T) and by Ctrl+\ Ctrl+n, until i or Ctrl+T returns to typing. A switch to
// Vim's keys takes the hint away.
func TestATextUsersNormalModeSaysHowToGetBack(t *testing.T) {
	_, r := runShellTUI(t, map[string]string{prefKeymap: "text"})
	catInTheTerminal(t, r)
	gone := func(sc string) bool { return !strings.Contains(sc, hintProbe) }

	r.keys(t, ctrlKey('t'))
	r.s.WaitForText(t, hintProbe)
	r.keys(t, ctrlKey('t'))
	r.s.WaitFor(t, "Ctrl+T back to typing: the hint gone", gone)

	r.keys(t, ctrlKey('\\'), ctrlKey('n'))
	r.s.WaitForText(t, hintProbe)
	r.keys(t, key('i'))
	r.s.WaitFor(t, "i back to typing: the hint gone", gone)

	r.keys(t, ctrlKey('t'))
	r.s.WaitForText(t, hintProbe)
	onLoop(r, func() bool { r.h.setKeymap("vim"); return true })
	r.s.WaitFor(t, "Vim's keys: the hint gone", gone)
	r.keys(t, key('i'))
	r.keys(t, decltest.Type("still-there\r")...)
	r.s.WaitFor(t, "the shell is still cat's", catPrinted("still-there"))
}

// The agent panel's terminals follow the keymap too: under the text editor's keys, its normal mode
// says how to get back.
func TestATextUsersAgentNormalModeSaysHowToGetBack(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUIWith(t, t.TempDir(), []agentProfile{{Name: "fake", Command: shellQuote(agent)}}, "fake",
		map[string]string{prefKeymap: "text"}, Options{})
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true }) // SPC g: the text editor's keys have no SPC
	r.s.WaitForText(t, "agent · fake")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	r.keys(t, ctrlKey('t'))
	r.s.WaitForText(t, hintProbe)
	r.keys(t, ctrlKey('t'))
	r.s.WaitFor(t, "back to typing", func(sc string) bool { return !strings.Contains(sc, hintProbe) })
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

// The terminal's strip ends in _ and x: _ hides the panel with its shell running on; x asks once while
// a shell runs, Escape keeps it, and Yes hangs it up and closes the panel.
func TestTheTerminalsStripHidesAndStopsAndCloses(t *testing.T) {
	_, r := runShellTUI(t, nil)
	catInTheTerminal(t, r)

	windowControl(t, r, "_")
	r.s.WaitFor(t, "_ hid the panel", func(string) bool { return !r.termShown() })
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "shown again", func(string) bool { return r.termShown() })
	r.keys(t, decltest.Type("kept\r")...)
	r.s.WaitFor(t, "the same cat, kept running", catPrinted("kept"))

	windowControl(t, r, "x")
	r.s.WaitForText(t, "stop the shells and close the terminal?")
	r.keys(t, esc())
	r.s.WaitFor(t, "Escape: the question gone", func(sc string) bool {
		return !strings.Contains(sc, "stop the shells and close the terminal?")
	})
	if !r.termShown() {
		t.Fatal("Escape on x's question closed the terminal")
	}
	windowControl(t, r, "x")
	r.s.WaitForText(t, "stop the shells and close the terminal?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "Yes: the terminal closed", func(string) bool { return !r.termShown() })
	if n := onLoop(r, func() int { p, _ := r.h.terminals.panel(); return len(p.Tabs()) }); n != 0 {
		t.Errorf("x left %d shells' tabs", n)
	}
}
