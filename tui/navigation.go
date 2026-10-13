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

// pendingJump is a step into another document, waiting for it to be shown.
type pendingJump struct {
	step widget.JumpStep
	ref  fileRef
}

// jumpView is the page's view, as the jump list records it: "rendered" in the GUI's Rendered view.
func (h *Host) jumpView() string { return editorView(h.editor) }

// jumpTo is the editor's step into another document or view: this document's other view at once,
// another document once it is shown (landJump).
func (h *Host) jumpTo(step widget.JumpStep) {
	ref := refOf(step.To.Doc)
	if h.file.open && ref == (fileRef{h.file.ws, h.file.path}) {
		h.landAt(step)
		return
	}
	h.jumpWait = &pendingJump{step: step, ref: ref}
	h.openRef(ref, false, nil)
}

// landJump is a document shown: the step waiting for it, if any, is committed and lands. A step for
// another document is dropped: a newer open went ahead.
func (h *Host) landJump(ref fileRef) {
	w := h.jumpWait
	h.jumpWait = nil
	if w != nil && w.ref == ref {
		h.landAt(w.step)
	}
}

// landAt commits step and puts the page where it goes: its view, its line and column. A step the
// list no longer takes (it moved since) changes nothing.
func (h *Host) landAt(step widget.JumpStep) {
	if !h.jumps.Commit(step) {
		return
	}
	setEditorView(h.editor, step.To.View)
	h.core.SetLine(step.To.Line, step.To.Col)
}

// missedJump is a read that failed for ref: a document gone for good leaves the list, and the user
// is told; any other failure leaves the list as it was.
func (h *Host) missedJump(ref fileRef, err error) {
	w := h.jumpWait
	if w == nil || w.ref != ref {
		return
	}
	h.jumpWait = nil
	if code(err) == rpc.CodeNotFound && h.jumps.Drop(w.step) {
		h.notify(ref.path + " is gone: it left the jump list (Ctrl+O again goes further back)")
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
