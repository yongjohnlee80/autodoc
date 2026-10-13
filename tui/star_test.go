package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/highlight"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// starKB is a page whose words repeat on a line: "foo" three times on the first, once on the
// second, and inside "foobar" on the third.
var starKB = map[string][]string{"kb": {
	"a.md", "foo foo foo\nbar foo\nfoobar",
	"b.md", "x\n",
}}

// waitCursorAt waits for the page's cursor at (line, col).
func (r *running) waitCursorAt(t *testing.T, what string, line, col int) {
	t.Helper()
	r.s.WaitFor(t, what, func(string) bool { return r.cursor() == [2]int{line, col} })
}

// * and # are the page's find, by occurrence: every hit on a line counts, from on a hit or
// between hits, with a count; n and N go on from there; the chip counts the occurrences.
func TestStarIsThePagesFindByOccurrence(t *testing.T) {
	r := attached(t, startDaemon(t, starKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.keys(t, decltest.Alt('2'), key('*')) // from foo at (0,0)
	r.waitCursorAt(t, "* to the next foo on the same line", 0, 4)
	r.s.WaitForText(t, `finding "foo" 2/4`)
	r.keys(t, key('n'))
	r.waitCursorAt(t, "n to the third foo on the line", 0, 8)
	r.keys(t, key('n'))
	r.waitCursorAt(t, "n to the next line's foo", 1, 4)
	r.keys(t, key('n'))
	r.waitCursorAt(t, "n wraps to the first, skipping foobar", 0, 0)
	r.keys(t, key('N'))
	r.waitCursorAt(t, "N back, wrapping", 1, 4)
	onLoop(r, func() bool { r.h.core.SetLine(0, 3); return true }) // between hits: on the blank
	r.keys(t, key('*'))
	r.waitCursorAt(t, "* from between hits: the next word's, then on", 0, 8)
	onLoop(r, func() bool { r.h.core.SetLine(0, 0); return true })
	r.keys(t, key('2'), key('*'))
	r.waitCursorAt(t, "2*", 0, 8)
	onLoop(r, func() bool { r.h.core.SetLine(0, 0); return true })
	r.keys(t, key('#'))
	r.waitCursorAt(t, "# wraps back", 1, 4)
	if term, _ := onLoopTerm(r); term != "" {
		t.Errorf("the editor kept a find of its own: %q", term)
	}
}

// g* finds the word inside others too; * doesn't, and the page's marks follow: foobar's foo is
// marked for g* and not for *.
func TestStarWholeWordsAndTheirMarks(t *testing.T) {
	r := attached(t, startDaemon(t, starKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.core.SetLine(1, 4); return true })
	r.keys(t, decltest.Alt('2'), key('*'))
	r.waitCursorAt(t, "* skips foobar", 0, 0)
	marked := func(line string) bool {
		return onLoop(r, func() bool {
			for _, s := range markedSpans(&r.h.findMarks, line, nil) {
				if s.Style == highlight.Alert {
					return true
				}
			}
			return false
		})
	}
	if marked("foobar") {
		t.Error("* marks foo inside foobar")
	}
	if !marked("bar foo") {
		t.Error("* doesn't mark a whole foo")
	}
	onLoop(r, func() bool { r.h.core.SetLine(1, 4); return true })
	r.keys(t, key('g'), key('*'))
	r.waitCursorAt(t, "g* finds foobar's foo", 2, 0)
	if !marked("foobar") {
		t.Error("g* doesn't mark foo inside foobar")
	}
}

// Replace's Ctrl+O shows (REPLACE) on the status line while its command runs.
func TestTheStatusLineSaysReplaceForCtrlO(t *testing.T) {
	r := attached(t, startDaemon(t, starKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.keys(t, decltest.Alt('2'), key('R'))
	r.s.WaitFor(t, "REPLACE on the status line", func(s string) bool { return strings.Contains(s, "REPLACE") })
	r.keys(t, decltest.Ctrl('o'))
	r.s.WaitForText(t, "(REPLACE)")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
}

func onLoopTerm(r *running) (string, bool) {
	return onLoop(r, func() string { t, _ := r.h.core.FindTerm(); return t }), true
}
