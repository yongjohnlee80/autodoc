package tui

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// relations_test.go: the Relations drawer (ADR 1791430651 §4.4) — each relation in its section by
// kind and direction, depth 2, the unresolved and the cycles; the jump card; back; and the editor's
// right-click rows.

// relationsKB is doc.md and what it is connected to: every relation kind out of it, two into it,
// three body links, one that names nothing, a supersession loop with loop.md, and two links away
// far.md (new.md links to it) and hub.md (it links to src.md): ten neighbours, one past the card.
var relationsKB = map[string][]string{"kb": {
	"doc.md", "---\nsuperseded_by: [new.md]\nsupersedes: [loop.md]\nsources: [src.md]\namends: [base.md]\nrelated: [rel.md]\n---\n# Doc\n\nsee [[body]], [[body2]], [[body3]] and [[missing-one]]\n",
	"new.md", "# New\n\n[[far]]\n",
	"far.md", "# Far\n",
	"loop.md", "---\nsupersedes: [doc.md]\n---\n# Loop\n",
	"old.md", "---\nsuperseded_by: [doc.md]\n---\n# Old\n",
	"src.md", "# Src\n",
	"citer.md", "---\nsources: [doc.md]\n---\n# Citer\n",
	"base.md", "# Base\n",
	"rel.md", "# Rel\n",
	"body.md", "# Body\n",
	"body2.md", "# Body 2\n",
	"body3.md", "# Body 3\n",
	"hub.md", "# Hub\n\n[[src]]\n",
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
	"links (3)", "  body.md", "  body2.md", "  body3.md",
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
	r.s.WaitForText(t, "relations (10)")
	if onLoop(r, func() bool { return r.h.relDeep }) {
		t.Fatal("d in the explorer turned the drawer's depth 2 on")
	}

	r.keys(t, key('d'))
	deep := slices.Insert(slices.Clone(docRelations), 2, "    › far.md  (wikilink →)")
	deep = slices.Insert(deep, slices.Index(deep, "  src.md")+1, "    › hub.md  (← wikilink)") // it links to src.md
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
// returns no further than the first document; the right-click menu has Back.
func TestJumpCardAndBack(t *testing.T) {
	d := startDaemon(t, relationsKB)
	r := attached(t, d)
	r.openByPicker(t, "doc.md")
	r.waitFile(t, "doc.md")
	r.s.WaitFor(t, "doc's relations", func(string) bool { return len(r.relationLabels()) > 0 })
	r.leader(t, 'j')
	r.s.WaitForText(t, "related documents")
	r.s.WaitForText(t, "3  supersedes     old.md")
	r.s.WaitForText(t, "9  links          body2.md") // ten neighbours: the card holds nine
	if strings.Contains(r.s.String(), "body3.md") {
		t.Error("the card holds a tenth neighbour")
	}
	r.keys(t, key('3'))
	r.waitFile(t, "old.md")

	r.leader(t, 'b')
	r.waitFile(t, "doc.md")
	r.leader(t, 'b')
	r.s.WaitForText(t, "nothing to go back to")

	// the right-click menu: Back goes back
	r.leader(t, 'j')
	r.s.WaitForText(t, "related documents")
	r.keys(t, key('1'))
	r.waitFile(t, "new.md")
	r.waitRelations(t, []string{"supersedes (1)", "  doc.md", "links (1)", "  far.md"}) // new.md's own, not doc.md's
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

// TestBackKeepsItsDocumentWhenStayIsChosen: back over unsaved changes asks first; Stay keeps the
// page and the history as they were, so back again asks again, and Discard then goes back.
func TestBackKeepsItsDocumentWhenStayIsChosen(t *testing.T) {
	d := startDaemon(t, relationsKB)
	r := attached(t, d)
	r.openByPicker(t, "doc.md")
	r.waitFile(t, "doc.md")
	r.s.WaitFor(t, "doc's relations", func(string) bool { return len(r.relationLabels()) > 0 })
	r.leader(t, 'j')
	r.s.WaitForText(t, "related documents")
	r.keys(t, key('1'))
	r.waitFile(t, "new.md")
	r.typeInEditor(t, "edit ")
	r.leader(t, 'b')
	r.s.WaitForText(t, "Save them before")
	r.h.p.Post(func() { r.h.unsaved("stay") }) // the dialog's Stay
	r.s.WaitFor(t, "the question closed", func(sc string) bool { return !strings.Contains(sc, "Save them before") })
	if f := r.file(); f.path != "new.md" || !f.dirty {
		t.Fatalf("Stay left %s (dirty %v), want new.md unsaved", f.path, f.dirty)
	}
	r.leader(t, 'b')
	r.s.WaitForText(t, "Save them before")
	r.h.p.Post(func() { r.h.unsaved("discard") })
	r.waitFile(t, "doc.md")
	if hist := onLoop(r, func() []fileRef { return append([]fileRef(nil), r.h.history...) }); len(hist) != 0 {
		t.Errorf("history after going back: %v, want it spent", hist)
	}
}

// TestTheJumpCardIsTheOpenDocuments: until the new document's relations are read, the card has
// none — never the previous document's — and once they are, it has the new one's.
func TestTheJumpCardIsTheOpenDocuments(t *testing.T) {
	d := startDaemon(t, relationsKB)
	sess := NewSession(d.sock, nil)
	hold := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(hold) }) }
	t.Cleanup(release)
	sess.beforeCall = func(method string, params []any) {
		if method == "graph.links" && len(params) > 1 && params[1] == "new.md" {
			<-hold
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	r.openByPicker(t, "doc.md")
	r.waitFile(t, "doc.md")
	r.s.WaitFor(t, "doc's relations", func(string) bool { return slices.Equal(r.relationLabels(), docRelations) })
	r.leader(t, 'j')
	r.s.WaitForText(t, "related documents")
	r.keys(t, key('1'))
	r.waitFile(t, "new.md") // its relations held
	r.leader(t, 'j')
	r.waitKeptNotice(t, "no related documents: SPC l shows what new.md links with")
	release()
	r.waitRelations(t, []string{"supersedes (1)", "  doc.md", "links (1)", "  far.md"})
	r.leader(t, 'j')
	r.s.WaitForText(t, "1  supersedes     doc.md")
}

// TestRelationsEdges: with no file open the drawer is empty; Enter on a heading opens nothing; the
// history keeps one copy of a document at its top and the last 32; back skips the page already
// open.
func TestRelationsEdges(t *testing.T) {
	d := startDaemon(t, relationsKB)
	r := attached(t, d)
	r.leader(t, 'l')
	r.s.WaitFor(t, "an empty drawer", func(string) bool { return len(r.relationLabels()) == 0 })
	r.keys(t, esc())

	r.openByPicker(t, "doc.md")
	r.waitFile(t, "doc.md")
	r.s.WaitFor(t, "doc's relations", func(string) bool { return slices.Equal(r.relationLabels(), docRelations) })
	gen := r.file().gen
	if g := onLoop(r, func() uint64 { r.h.openRelation(0); return r.h.file.gen }); g != gen {
		t.Errorf("Enter on the heading %q opened something", docRelations[0])
	}

	hist := onLoop(r, func() []fileRef {
		r.h.history = nil
		a := fileRef{"kb", "a.md"}
		r.h.rememberLeft(a)
		r.h.rememberLeft(a)
		for i := range maxHistory + 5 {
			r.h.rememberLeft(fileRef{"kb", fmt.Sprintf("n%d.md", i)})
		}
		return append([]fileRef(nil), r.h.history...)
	})
	if len(hist) != maxHistory || hist[len(hist)-1].path != fmt.Sprintf("n%d.md", maxHistory+4) {
		t.Errorf("history %d long ending %v, want the last %d", len(hist), hist[len(hist)-1], maxHistory)
	}
	two := onLoop(r, func() []fileRef {
		r.h.history = nil
		r.h.rememberLeft(fileRef{"kb", "a.md"})
		r.h.rememberLeft(fileRef{"kb", "a.md"})
		return append([]fileRef(nil), r.h.history...)
	})
	if len(two) != 1 {
		t.Errorf("leaving a.md twice in a row: %v, want it once", two)
	}

	// the page itself on top (old.md, then doc.md): back goes to old.md, past it
	r.h.p.Post(func() { r.h.history = []fileRef{{"kb", "old.md"}, {"kb", "doc.md"}}; r.h.goBack() })
	r.waitFile(t, "old.md")
	if h := onLoop(r, func() int { return len(r.h.history) }); h != 0 {
		t.Errorf("history after going back past the page: %d entries, want none", h)
	}
}

// The editor's right-click menu has View diagram, enabled when the file has a Mermaid block to
// show, as File › Preview Mermaid diagram does.
func TestTheEditorMenuViewsTheDiagram(t *testing.T) {
	r := attached(t, startDaemon(t, map[string][]string{"kb": {
		"a.md", "# A\n",
		"d.md", "# D\n\n```mermaid\nflowchart LR\n  A --> B\n```\n",
	}}))
	// item is View diagram's row in p's menu: whether there is one, and whether it is enabled
	item := func(p string) (found, on bool) {
		r.h.p.Post(func() { r.h.openPath(p) })
		r.waitFile(t, p)
		got := onLoop(r, func() [2]bool {
			for _, it := range r.h.editorMenu(r.h.core) {
				if it.ID == "autodoc.diagram" {
					return [2]bool{true, it.Enabled}
				}
			}
			return [2]bool{}
		})
		return got[0], got[1]
	}
	if found, on := item("a.md"); !found || on {
		t.Errorf("a.md, no diagram: View diagram found %v, enabled %v; want found, disabled", found, on)
	}
	if found, on := item("d.md"); !found || !on {
		t.Errorf("d.md, a diagram: View diagram found %v, enabled %v; want found, enabled", found, on)
	}
	r.menuRow(t, "View diagram")
	r.s.WaitForText(t, "Diagram source")
}

// linkAt finds the link the cursor is on, of each kind, and nothing elsewhere.
func TestLinkAtTheCursor(t *testing.T) {
	src := "see [the doc](doc.md) and [[far|Far away]], <https://example.com>, [[#Heading]].\n"
	at := func(s string) int { return strings.Index(src, s) }
	for _, c := range []struct {
		at        int
		raw, dest string
		wiki, ok  bool
	}{
		{at("the doc"), "[the doc](doc.md)", "doc.md", false, true},
		{at("Far away"), "[[far|Far away]]", "far", true, true},
		{at("example"), "<https://example.com>", "https://example.com", false, true},
		{at("see"), "", "", false, false},
		{at("Heading"), "", "", false, false}, // a heading of this note names no document
	} {
		l, ok := linkAt([]byte(src), c.at)
		if ok != c.ok || (ok && (l.raw != c.raw || l.dest != c.dest || l.wiki != c.wiki)) {
			t.Errorf("at %d: %+v %v, want %q %q wiki %v %v", c.at, l, ok, c.raw, c.dest, c.wiki, c.ok)
		}
	}
}

// Go to link, on the right-click menu, is there only on a link, and opens the document the
// daemon resolves it to now: one just typed, unsaved, too. A wikilink to no document says so.
func TestGoToLinkOpensTheLinkedDocument(t *testing.T) {
	r := attached(t, startDaemon(t, relationsKB))
	r.h.p.Post(func() { r.h.openPath("new.md") })
	r.waitFile(t, "new.md")
	// row puts the cursor at line, col and reads the menu's Go to link: enabled, and its link
	row := func(line, col int) (bool, cursorLink) {
		got := onLoop(r, func() [2]any {
			r.h.core.SetLine(line, col)
			l, _ := linkAt([]byte(r.h.core.Value()), r.h.cursorBytes())
			for _, it := range r.h.editorMenu(r.h.core) {
				if it.ID == "autodoc.link" {
					return [2]any{it.Enabled, l}
				}
			}
			return [2]any{false, l}
		})
		return got[0].(bool), got[1].(cursorLink)
	}
	if on, _ := row(0, 2); on {
		t.Error("Go to link is enabled on the heading")
	}
	on, l := row(2, 3)
	if !on {
		t.Fatal("Go to link is not enabled on [[far]]")
	}
	if !onLoop(r, func() bool {
		for _, item := range r.h.editorMenu(r.h.core) {
			if item.ID == "autodoc.link" {
				item.Action.(widget.CoreMenuAction).Run(r.h.core)
				return true
			}
		}
		return false
	}) {
		t.Fatal("Go to link is absent from the editor menu")
	}
	r.waitFile(t, "far.md")

	// a link typed into far.md and not saved: the index has never seen it
	onLoop(r, func() bool { r.h.core.SetValue("# Far\n\nsee [[Body2|the second]]\n"); return true })
	on, l = row(2, 7)
	if !on {
		t.Fatal("Go to link is not enabled on the unsaved [[Body2|the second]]")
	}
	r.h.p.Post(func() { r.h.goToLink(l) })
	r.waitFile(t, "body2.md")

	r.h.p.Post(func() { r.h.goToLink(cursorLink{raw: "[[missing-one]]", dest: "missing-one", wiki: true}) })
	r.waitNoticed(t, "[[missing-one]] names no document in this workspace")
}

// An answer that comes after another file opened is the old file's: dropped, not followed from the
// new one.
func TestGoToLinkDropsAnAnswerForAFileNoLongerOpen(t *testing.T) {
	r := attached(t, startDaemon(t, relationsKB))
	r.h.p.Post(func() { r.h.openPath("new.md") })
	r.waitFile(t, "new.md")
	// asked in new.md, and doc.md opened in the same turn of the loop: the answer comes after
	r.h.p.Post(func() {
		r.h.goToLink(cursorLink{raw: "[[far]]", dest: "far", wiki: true})
		r.h.openPath("doc.md")
	})
	r.waitFile(t, "doc.md")
	time.Sleep(300 * time.Millisecond) // the answer's time to arrive, were it followed
	if p := onLoop(r, func() string { return r.h.file.path }); p != "doc.md" {
		t.Fatalf("the late answer opened %s from doc.md", p)
	}
}
