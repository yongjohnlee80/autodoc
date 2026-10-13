package tui

import (
	"strings"

	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// NAVIGATION — Vim's jump list across the documents (golib's widget.JumpList, Ctrl+O back and
// Ctrl+I forward in the editor's Normal mode). Every document opened that is remembered (a link,
// a picker, the explorer, the relations, the recent files) is a jump: the place it leaves goes on
// the list once the new document is shown. The editor's own jumps (G, gg, %, { }, a mark's, …)
// join it. A step into another document, or another view of this one, is the host's (jumpTo): it
// opens the document, and commits the step once it is shown there; an open that doesn't go ahead
// (Stay at the unsaved question, an HTML file's Cancel, a failed read, a newer open) leaves the list
// as it was. SPC b goes back to the nearest place in another document, over the same list.

// fileRef is a document opened: its workspace ("" outside every one) and its path.
type fileRef struct{ ws, path string }

// docKey is a document as the jump list knows it.
func docKey(r fileRef) string { return r.ws + "\x00" + r.path }

// refOf is the document a jump list's key names.
func refOf(doc string) fileRef {
	ws, p, _ := strings.Cut(doc, "\x00")
	return fileRef{ws, p}
}

// landing is where the cursor goes when an open is shown: a byte offset in its text (a search
// hit's section, a draft just saved), or a jump step's line, column and view, committed then. It
// travels with its own open (openRefTo, loadTo) and no other: an open that doesn't go ahead (Stay
// at the unsaved question, an HTML file's Cancel, a failed read, a newer open's ticket) drops it,
// so it never lands on another document, or on a later open of the same one.
type landing struct {
	at   int              // a byte offset, when step is nil
	step *widget.JumpStep // a jump's
}

// jumpView is the page's view, as the jump list records it: "rendered" in the GUI's Rendered view.
func (h *Host) jumpView() string { return editorView(h.editor) }

// jumpTo is the editor's step into another document or view: it opens the document, the step
// landing when it is shown (a document shown already lands at once).
func (h *Host) jumpTo(step widget.JumpStep) {
	h.openRefTo(refOf(step.To.Doc), false, nil, &landing{step: &step})
}

// land puts the cursor where land says, on ref just shown (its text content): a jump step is
// committed first, and lands only if the list takes it (it moved since: nothing changes).
func (h *Host) land(land *landing, ref fileRef, content string) {
	switch {
	case land == nil:
	case land.step != nil:
		if !h.jumps.Commit(*land.step) {
			return
		}
		setEditorView(h.editor, land.step.To.View)
		h.core.SetLine(land.step.To.Line, land.step.To.Col)
	default:
		h.core.SetCursorPosition(cursorAt(content, min(max(land.at, 0), len(content))))
	}
}

// missedLanding is an open whose read failed: a jump's document gone for good leaves the list, and
// the user is told; any other failure leaves the list as it was.
func (h *Host) missedLanding(land *landing, err error) {
	if land == nil || land.step == nil || code(err) != rpc.CodeNotFound {
		return
	}
	if h.jumps.Drop(*land.step) {
		h.notify(refOf(land.step.To.Doc).path + " is gone: it left the jump list (Ctrl+O again goes further back)")
	}
}

// backToDocument is the nearest step back to another document, and whether there is one.
func (h *Host) backToDocument() (widget.JumpStep, bool) {
	at := h.core.JumpHere()
	for n := 1; ; n++ {
		step, ok := h.jumps.Back(at, n)
		if !ok {
			return widget.JumpStep{}, false
		}
		if step.To.Doc != at.Doc {
			return step, true
		}
	}
}

// goBack is SPC b: back to the document opened before this one, at the place it was left.
func (h *Host) goBack() {
	if step, ok := h.backToDocument(); ok {
		h.jumpTo(step)
		return
	}
	h.notify("nothing to go back to: SPC b returns to the documents opened before this one")
}
