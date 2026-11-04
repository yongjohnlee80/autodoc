//go:build !gui

package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

const notePageNote = "# Pantry\n\nSome **bold** words.\n\n| Item | Qty |\n| --- | ---: |\n| apple | 3 |\n\n" +
	"```mermaid\nmindmap\n  root((Larder))\n    Shelf\n    Bin\n```\n\n```mermaid\nflowchart LR\n  A-->B\n```\n"

// TestATerminalNoteIsItsPage (docs/decisions/editor.md#a-terminal-notes-rendered-view): a note
// opens Rendered as its page in cells (its table boxed, its mind map drawn, another diagram said
// not to be drawn here), Ctrl+T shows its text and back its page, and an edit while the page is
// shown draws it again once typing rests.
func TestATerminalNoteIsItsPage(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "pantry.md", notePageNote)}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	view := func() (widget.EditorView, bool) {
		type shown struct {
			v    widget.EditorView
			page bool
		}
		s := onLoop(r, func() shown {
			e := r.h.editor.(*widget.Editor)
			_, page := e.DocumentView().(*notePageView)
			return shown{e.ShownView(), page}
		})
		return s.v, s.page
	}
	ctrlT := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl}

	onLoop(r, func() bool { r.h.openPath("pantry.md"); return true })
	r.waitFile(t, "pantry.md")
	r.s.WaitFor(t, "the note shown as its page", func(string) bool { v, page := view(); return page && v == widget.ViewRendered })
	r.s.WaitForText(t, "Pantry")
	r.s.WaitForText(t, "┌") // the table, boxed
	r.s.WaitForText(t, "Larder")
	r.s.WaitForText(t, "flowchart diagram: not drawn in the terminal (Raw shows its source)")
	if strings.Contains(r.s.String(), "**bold**") || strings.Contains(r.s.String(), "A-->B") {
		t.Fatalf("the page shows the note's source:\n%s", r.s.String())
	}

	r.keys(t, ctrlT)
	r.s.WaitFor(t, "the note's text after Ctrl+T", func(string) bool { v, _ := view(); return v == widget.ViewRaw })
	r.s.WaitForText(t, "**bold**")
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "its page again", func(string) bool { v, _ := view(); return v == widget.ViewRendered })
	r.s.WaitForText(t, "Larder")

	// an edit while the page is shown (an agent's, say): drawn once typing rests
	onLoop(r, func() bool {
		if _, err := r.h.core.Insert(widget.TextPos{}, "# Larder notes\n\n"); err != nil {
			t.Error(err)
		}
		return true
	})
	r.s.WaitForText(t, "Larder notes")
}

// TestTheTerminalNotePagesNoticeIsTheCatalogs: the line a diagram the terminal doesn't draw shows is
// the catalog's, its kind filled in (the page asks it at each layout, so a language switch's
// relayout says it in the new language).
func TestTheTerminalNotePagesNoticeIsTheCatalogs(t *testing.T) {
	p := newCellNotePage(newHost(NewSession("unused", nil), Options{}))
	if got := p.notice("flowchart"); got != "flowchart diagram: not drawn in the terminal (Raw shows its source)" {
		t.Errorf("the English notice: %q", got)
	}
	if !strings.Contains(p.h.trf(diagramNoticeID, "%s"), "%s") {
		t.Errorf("the notice takes no kind: %q", p.h.trf(diagramNoticeID, "%s"))
	}
}
