package tui

import (
	"context"
	"path/filepath"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/kind"
)

// A NOTE'S RENDERED VIEW, under --gui: the note as a page, drawn natively by the editor's document
// view (golib's HTMLView), as the HTML preview draws it: its embeds of a Mermaid diagram, a CSV
// table or an image drawn in place (core/export's embed.go), its raw HTML, its links. The page is
// read-only; Ctrl+T shows the note's source to edit, and showing the page again renders what was
// typed. A raw HTML file's page is its own text, shown as it is (htmldoc.go). The untitled draft
// and a derived document keep the editor's own Rendered view, which draws Markdown and is
// editable.

// noteTableRows is how many rows a note's page shows of a table it embeds; its columns are those
// that fit the page's width (the ruler). The whole table is the HTML preview's, or xmd's.
const noteTableRows = 16

// NoteDocumentView is the GUI editor's document view, which the gui style makes for main.qml's
// HTMLDocumentView (app's runGUI): golib's HTMLView, whose page for a note the host renders. A text
// the host does not render (a raw HTML file's) is the page itself.
type NoteDocumentView struct {
	*widget.HTMLView
	// show is the host's: it renders text as the page and reports true, or reports false for a text
	// that is the page already. nil: every text is the page.
	show func(text []byte) bool
}

// NewNoteDocumentView is v as the editor's document view.
func NewNoteDocumentView(v *widget.HTMLView) *NoteDocumentView { return &NoteDocumentView{HTMLView: v} }

// pageOf is a document view's HTMLView: golib's, or the one a NoteDocumentView wraps.
func pageOf(c tuicore.Component) (*widget.HTMLView, bool) {
	switch v := c.(type) {
	case *NoteDocumentView:
		return v.HTMLView, true
	case *widget.HTMLView:
		return v, true
	}
	return nil, false
}

// linkNote has the editor's document view, when it is a NoteDocumentView, ask the host to render
// a note's text (showNote).
func (h *Host) linkNote() {
	if dc, ok := h.editor.(documentComponent); ok {
		linkNoteDocument(dc.DocumentComponent(), h.showNote)
	}
}

// linkNoteDocument gives c, when it is a NoteDocumentView, show; it reports whether it is one.
func linkNoteDocument(c tuicore.Component, show func(text []byte) bool) bool {
	d, ok := c.(*NoteDocumentView)
	if ok {
		d.show = show
	}
	return ok
}

// ShowDocument shows text: the page the host renders from it, or text itself.
func (d *NoteDocumentView) ShowDocument(text []byte) {
	if d.show != nil && d.show(text) {
		return
	}
	d.HTMLView.ShowDocument(text)
}

// noteDocState is the note whose page the document view shows: none (on false) for any other
// document. The note's file is set as it opens, before its text reaches the editor.
type noteDocState struct {
	on   bool
	file string // the note's absolute path on disk; "" when its workspace's root is not known yet
	gen  uint64 // numbers the renders; a later one supersedes an earlier
}

// isNote reports whether p, opened not raw, is a note whose Rendered view is its page: a file
// read as Markdown.
func (h *Host) isNote(p string, raw bool) bool {
	return p != "" && !raw && h.resolver().Of(p) == kind.Markdown
}

// showNote renders text, the note's Markdown, as its page, off the loop, and shows it when it is
// ready, the cursor's block at the top; a later text's page supersedes it. It reports false when the
// view shows no note: the text is the page.
func (h *Host) showNote(text []byte) bool {
	if !h.noteDoc.on {
		return false
	}
	tk := h.ticket(&h.noteDoc.gen, TicketLocal)
	source, theme := append([]byte(nil), text...), h.exportTheme()
	options := export.Options{SourceSpans: true, TableRows: noteTableRows, TableWidth: h.prefs.ruler}
	if full := h.noteDoc.file; full != "" {
		options.Base, options.Document = filepath.Dir(full), full
	}
	do(h, func(context.Context) answerOf[[]byte] {
		return answered(export.RenderWith(source, export.HTML, theme, options))
	}, func(a answerOf[[]byte]) {
		if !tk.Current() {
			return // a later text: its page follows
		}
		if a.err != nil {
			h.failed("Rendered view", a.err)
			return
		}
		h.setNotePage(a.v)
	})
	return true
}

// setNotePage shows page in the document view, the cursor's block at the top.
func (h *Host) setNotePage(page []byte) {
	if h.notePage != nil {
		h.notePage(page)
		return
	}
	if v, ok := h.pageView(); ok {
		v.SetHTML(page)
		v.ScrollToSource(h.cursorBytes())
	}
}
