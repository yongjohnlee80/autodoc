package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
	"github.com/yongjohnlee80/golib/tui/widget/mergeview"
)

// compare_window_test.go: the conflict's Compare in a window (Options.GUI), which offers it as a
// terminal does and draws golib's MergeView in cells. Built with the gui tag, these run with the
// window's native style.

var asWindow = Options{GUI: true}

// screenAt is where text first shows on the screen, in cells, on a row below the first that holds
// below.
func screenAt(screen, below, text string) (x, y int, ok bool) {
	under := below == ""
	for y, row := range strings.Split(screen, "\n") {
		if !under {
			under = strings.Contains(row, below)
			continue
		}
		if i := strings.Index(row, text); i >= 0 {
			return len([]rune(row[:i])), y, true
		}
	}
	return 0, 0, false
}

// comparePane is what the cells need of a Compare pane.
type comparePane interface {
	tuicore.Component
	Core() *editor.EditorCore
}

// focusPane puts the focus in the Compare's pane (0 the disk's, 1 the result, 2 the buffer's), its
// cursor on line.
func (r *running) focusPane(t *testing.T, v *mergeview.MergeView, pane, line int) {
	t.Helper()
	onLoop(r, func() bool {
		l, res, rt := v.Panes()
		p := [3]comparePane{l, res, rt}[pane]
		r.h.p.App().FocusInto(p)
		p.Core().SetLine(line, 0)
		return true
	})
}

// A window's conflict asks the question that offers Compare, with its button, and its Compare
// merges as a terminal's: the disk's change elsewhere merged in, a conflict left keeps the buffer's
// own lines, and Apply puts the result in the buffer, unsaved, at the disk's version.
func TestAWindowsCompareKeepsTheBuffersLinesInAConflictLeft(t *testing.T) {
	t.Parallel()
	r, root := conflictedAs(t, "ONE\ntwo\nthree disk\n", asWindow)
	r.s.WaitForText(t, "compare") // the question's own word, as it wraps: the button and the title say "Compare"
	r.s.WaitForText(t, "Compare")
	v := r.compareOpen(t)
	onLoop(r, func() bool { v.Merge(); v.Apply(); return true })
	r.s.WaitForText(t, "Apply anyway?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "the merge in the buffer", func(string) bool { return r.editorText() == "ONE\ntwo\nTHREE mine\n" })
	disk, _ := os.ReadFile(filepath.Join(root, "a.md"))
	if f := r.file(); !f.dirty || f.disk != diskSame || !f.baseOK || f.base != string(disk) {
		t.Fatalf("after Apply: dirty %v, disk %v, base %q (ok %v); want unsaved, at the disk's version, its text the base", f.dirty, f.disk, f.base, f.baseOK)
	}
}

// Text typed in a window's Compare result is what Apply puts in the buffer.
func TestTextTypedInAWindowsCompareResultIsApplied(t *testing.T) {
	t.Parallel()
	r, _ := conflictedAs(t, "ONE\ntwo\nthree\n", asWindow)
	v := r.compareOpen(t)
	r.focusPane(t, v, 1, 1)
	r.keys(t, key('i'), key('X'), esc())
	r.s.WaitFor(t, "the typed X in the result", func(string) bool {
		return onLoop(r, func() bool { return strings.Contains(v.Result(), "Xtwo") })
	})
	onLoop(r, func() bool { v.Merge(); v.Apply(); return true })
	r.s.WaitFor(t, "the result in the buffer", func(string) bool { return r.editorText() == "ONE\nXtwo\nTHREE mine\n" })
}

// A click on a window's Compare arrow takes that side's change: » takes the disk's first line.
func TestAClickOnAWindowsCompareArrowTakesTheChange(t *testing.T) {
	t.Parallel()
	r, _ := conflictedAs(t, "ONE\ntwo\nthree\n", asWindow)
	v := r.compareOpen(t)
	r.s.WaitForText(t, "»")
	x, y, ok := screenAt(r.s.String(), "Compare a.md with the disk", "»")
	if !ok {
		t.Fatal("no » on the screen")
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.s.WaitFor(t, "the disk's line taken", func(string) bool {
		return onLoop(r, func() bool { return strings.HasPrefix(v.Result(), "ONE\n") })
	})
}

// Ctrl+Shift+Right in a window's Compare takes the disk's change at the cursor, and Ctrl+Shift+Left
// the buffer's: both resolved, the window's own keys never take them.
func TestCtrlShiftArrowsTakeASideInAWindowsCompare(t *testing.T) {
	t.Parallel()
	r, _ := conflictedAs(t, "ONE\ntwo\nthree\n", asWindow)
	v := r.compareOpen(t)
	r.focusPane(t, v, 0, 0)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyRight, Mods: tuicore.ModCtrl | tuicore.ModShift})
	r.s.WaitFor(t, "the disk's change taken", func(string) bool {
		return onLoop(r, func() bool { return strings.HasPrefix(v.Result(), "ONE\n") })
	})
	r.focusPane(t, v, 2, 2)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyLeft, Mods: tuicore.ModCtrl | tuicore.ModShift})
	r.s.WaitFor(t, "both changes resolved", func(string) bool {
		return onLoop(r, func() bool { ch, _ := v.Counts(); return ch == 0 })
	})
	if got := onLoop(r, v.Result); got != "ONE\ntwo\nTHREE mine\n" {
		t.Fatalf("the result %q, want the disk's first line and the buffer's third", got)
	}
}

// The wheel in a window's Compare pane scrolls the others with it: 120 lines, the buffer's edit at
// the third and the disk's at the hundredth, so every pane has the same lines above.
func TestTheWheelInAWindowsComparePaneScrollsTheOthers(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := range 120 {
		lines = append(lines, fmt.Sprintf("row %03d", i))
	}
	text := strings.Join(lines, "\n") + "\n"
	r, root := onWideDiskAs(t, text, asWindow)
	r.openOnDisk(t, "a.md")
	onLoop(r, func() bool {
		_, _ = r.h.core.Replace(editor.TextRange{From: editor.TextPos{Line: 2}, To: editor.TextPos{Line: 2, Col: 7}}, "row 002 mine")
		return true
	})
	r.s.WaitFor(t, "the edit, unsaved", func(string) bool { return r.file().dirty })
	disk := append([]string{}, lines...)
	disk[100] = "row 100 disk"
	rewrite(t, root, "a.md", strings.Join(disk, "\n")+"\n")
	r.s.WaitFor(t, "a.md badged", func(string) bool { return diskOf(r, "a.md") == diskChanged })
	r.openOnDisk(t, "b.md")
	r.openByPicker(t, "a.md")
	r.s.WaitForText(t, "while it had unsaved changes")
	v := r.compareOpen(t)
	r.s.WaitForText(t, "row 010")
	x, y, ok := screenAt(r.s.String(), "Compare a.md with the disk", "row 010") // the left pane's, the first on its row
	if !ok {
		t.Fatal("no row 010 on the screen")
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelDown, X: x, Y: y})
	var tops [3]int
	r.s.WaitFor(t, "the panes scrolled together", func(string) bool {
		tops = onLoop(r, func() [3]int {
			l, res, rt := v.Panes()
			return [3]int{l.TopLine(), res.TopLine(), rt.TopLine()}
		})
		return tops[0] > 0 && tops[1] == tops[0] && tops[2] == tops[0]
	})
}
