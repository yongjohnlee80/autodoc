package tui

import (
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// relations_test.go: the Relations drawer (ADR 1791430651 §4.4) — each relation in its section by
// kind and direction, depth 2, the unresolved and the cycles; the jump card; back; and the editor's
// right-click rows.

// relationsKB is doc.md and what it is connected to: every relation kind out of it, two into it, a
// body link, one that names nothing, a supersession loop with loop.md, and far.md two links away.
var relationsKB = map[string][]string{"kb": {
	"doc.md", "---\nsuperseded_by: [new.md]\nsupersedes: [loop.md]\nsources: [src.md]\namends: [base.md]\nrelated: [rel.md]\n---\n# Doc\n\nsee [[body]] and [[missing-one]]\n",
	"new.md", "# New\n\n[[far]]\n",
	"far.md", "# Far\n",
	"loop.md", "---\nsupersedes: [doc.md]\n---\n# Loop\n",
	"old.md", "---\nsuperseded_by: [doc.md]\n---\n# Old\n",
	"src.md", "# Src\n",
	"citer.md", "---\nsources: [doc.md]\n---\n# Citer\n",
	"base.md", "# Base\n",
	"rel.md", "# Rel\n",
	"body.md", "# Body\n",
}}

func (r *running) relationLabels() []string {
	return onLoop(r, func() []string {
		var out []string
		for _, row := range r.h.relRows {
			out = append(out, row.label)
		}
		return out
	})
}

// waitRelations waits for the drawer's rows to be want.
func (r *running) waitRelations(t *testing.T, want []string) {
	t.Helper()
	r.s.WaitFor(t, "the relations "+strings.Join(want, " | "), func(string) bool { return slices.Equal(r.relationLabels(), want) })
}

var docRelations = []string{
	"superseded by (2)", "  new.md", "  loop.md",
	"supersedes (2)", "  loop.md", "  old.md",
	"sources (1)", "  src.md",
	"cited by (1)", "  citer.md",
	"amends (1)", "  base.md",
	"related (1)", "  rel.md",
	"links (1)", "  body.md",
	"unresolved (2)", "  [[missing-one]]  (wikilink, names no document)", "  loop.md  (supersession goes round a loop)",
}

// TestTheRelationsDrawerGroupsByKindAndDirection: SPC l lists each relation in its section — a
// relation into the document under its own side's name — the body's links, then what names
// nothing and the supersession loop; d adds the second ring under its first-ring document; Enter
// opens a row and the drawer stays open (§5.8).
func TestTheRelationsDrawerGroupsByKindAndDirection(t *testing.T) {
	d := startDaemon(t, relationsKB)
	r := attached(t, d)
	r.openByPicker(t, "doc.md")
	r.waitFile(t, "doc.md")
	// d is the drawer's alone: in the explorer it changes nothing there
	r.leader(t, 'e')
	r.s.WaitForText(t, "┌ explorer")
	r.keys(t, key('d'), esc())
	r.s.WaitFor(t, "the explorer closed", func(sc string) bool { return !strings.Contains(sc, "┌ explorer") })
	r.leader(t, 'l')
	r.waitRelations(t, docRelations)
	r.s.WaitForText(t, "relations (8)")
	if onLoop(r, func() bool { return r.h.relDeep }) {
		t.Fatal("d in the explorer turned the drawer's depth 2 on")
	}

	r.keys(t, key('d'))
	deep := slices.Insert(slices.Clone(docRelations), 2, "    › far.md  (wikilink →)")
	r.waitRelations(t, deep)
	r.s.WaitForText(t, "depth 2")
	r.keys(t, key('d'))
	r.waitRelations(t, docRelations)

	r.keys(t, enter()) // the cursor starts on the first document: new.md
	r.waitFile(t, "new.md")
	r.waitRelations(t, []string{"supersedes (1)", "  doc.md", "links (1)", "  far.md"}) // new.md's own
	if !onLoop(r, func() bool { return r.h.panelOpen["links"] }) {
		t.Error("opening a relation closed the drawer")
	}
}

// TestJumpCardAndBack: SPC j numbers the neighbours, a digit opens one; SPC b returns, and
// returns no further than the first document; the right-click menu has both.
func TestJumpCardAndBack(t *testing.T) {
	d := startDaemon(t, relationsKB)
	r := attached(t, d)
	r.openByPicker(t, "doc.md")
	r.waitFile(t, "doc.md")
	r.s.WaitFor(t, "doc's relations", func(string) bool { return len(r.relationLabels()) > 0 })
	r.leader(t, 'j')
	r.s.WaitForText(t, "related documents")
	r.s.WaitForText(t, "3  supersedes     old.md")
	r.keys(t, key('3'))
	r.waitFile(t, "old.md")

	r.leader(t, 'b')
	r.waitFile(t, "doc.md")
	r.leader(t, 'b')
	r.s.WaitForText(t, "nothing to go back to")

	// the right-click menu: Go to related… opens the card, Back goes back
	r.leader(t, 'j')
	r.s.WaitForText(t, "related documents")
	r.keys(t, key('1'))
	r.waitFile(t, "new.md")
	r.s.WaitFor(t, "new's relations", func(string) bool { return len(r.relationLabels()) > 0 })
	r.menuRow(t, "Go to related…")
	r.s.WaitForText(t, "related documents")
	r.s.WaitForText(t, "1  supersedes     doc.md")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "related documents") })
	r.menuRow(t, "Back")
	r.waitFile(t, "doc.md")
}

// menuRow right-clicks the page and clicks the menu's row label.
func (r *running) menuRow(t *testing.T, label string) {
	t.Helper()
	click := func(b tuicore.MouseButton, x, y int) {
		r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: b, X: x, Y: y},
			tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: b, X: x, Y: y})
	}
	click(tuicore.MouseRight, 10, 3)
	r.s.WaitForText(t, label)
	x, y := r.textAt(label)
	click(tuicore.MouseLeft, x, y)
}

// TestAnOutsideFileHasNoRelations: the drawer says why it is empty, and the card has nothing.
func TestAnOutsideFileHasNoRelations(t *testing.T) {
	r, _, _, abs := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	r.leader(t, 'l')
	r.s.WaitForText(t, "relations · "+outsideBadge)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.leader(t, 'j')
	r.s.WaitForText(t, "no related documents")
}
