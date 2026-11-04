package tui

import (
	"context"
	"path/filepath"

	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/kind"
)

// A NOTE'S RENDERED VIEW IN A TERMINAL (docs/decisions/editor.md#a-terminal-notes-rendered-view):
// on a terminal backend, in either build (a GUI build run with --ui too), Raw is the note's text,
// Rendered the page, as in the window. A window keeps its own (native) path: this does nothing there. The page is the note exported in
// process, as the HTML pane's is, and laid out in cells by golib's HTMLView: tables boxed, headings
// by level, code set off, a mind map drawn, any other diagram a line saying it isn't drawn here, in
// the UI's language. Ctrl+T flips between the two, as the window's does; the page shows the
// cursor's block when it is shown, and keeps the reader's place when the note changes under it.
// Its links open as the pane's do.

// cellNotePage is the feature: one page view, the editor's document view while a note is shown.
type cellNotePage struct {
	h      *Host
	view   *notePageView
	gen    uint64 // numbers the renders; a later one supersedes an earlier
	shown  fileRef
	dirKey string // the folder the page's images load from, as last given to the view
	dirSet bool
	// top is the source byte of the block at the page's top, as it last reported it (-1 none);
	// shownTop is top as the page was scrolled to the cursor: a page hidden at another top was
	// scrolled by the reader, and Raw's cursor follows it there (hidden).
	top, shownTop int
}

// notePageView is the page as the editor's document view: shown, it renders the note's text,
// which is what the editor hands it.
type notePageView struct {
	*widget.HTMLView
	page *cellNotePage
}

// ShowDocument renders text, the note, as its page: the editor calls it as the view is shown.
func (v *notePageView) ShowDocument(text []byte) { v.page.render(text, true) }

// SetVisible shows or hides the page; hidden (Ctrl+T to Raw), the cursor goes where the reader
// scrolled to.
func (v *notePageView) SetVisible(on bool) {
	v.HTMLView.SetVisible(on)
	if !on {
		v.page.hidden()
	}
}

var _ widget.DocumentView = (*notePageView)(nil)

func newCellNotePage(h *Host) *cellNotePage { return &cellNotePage{h: h, top: -1, shownTop: -1} }

func (*cellNotePage) Name() string                 { return "notepage" }
func (*cellNotePage) State() map[string]any        { return map[string]any{} }
func (*cellNotePage) Commands() map[string]Command { return map[string]Command{} }
func (*cellNotePage) Moved(int, int)               {}
func (*cellNotePage) Reread(Page)                  {}
func (*cellNotePage) Closed()                      {}
func (p *cellNotePage) notice(kind string) string  { return p.h.trf(diagramNoticeID, kind) }
func (p *cellNotePage) isShown() bool              { return p.view != nil && p.view.Visible() }

// onATerminal reports whether the program draws in a terminal's cells: not a window's native views,
// and the editor the terminal's (preview.go's showDiagram decides the same way).
func (p *cellNotePage) onATerminal() bool {
	_, ok := p.h.editor.(*widget.Editor)
	return ok && !p.h.native()
}
func (p *cellNotePage) LanguageChanged() { p.giveNotice() }

// giveNotice gives the page its words for a diagram it doesn't draw, in the language now: when it is
// made (viewOf) and as the language changes. Until a note is first shown there is no page, which is a
// state of its own (the page is made on first use): a language applied at start (applyPrefs) tells it
// nothing, and the page made later is given the words of that moment.
func (p *cellNotePage) giveNotice() {
	if p.view == nil {
		return
	}
	widget.WithDiagramNotice(p.notice)(p.view.HTMLView)
}

// diagramNoticeID is what a diagram the terminal doesn't draw shows: "⟨kind⟩ diagram: not drawn in
// the terminal (Raw shows its source)".
const diagramNoticeID = "autodoc.text.rendered_tui.kind_diagram_not_drawn_in_the_terminal"

// viewOf is the page view, made on first use: its links open as the pane's do, and a diagram it
// doesn't draw is said in the UI's language.
func (p *cellNotePage) viewOf() *notePageView {
	if p.view == nil {
		hv := widget.NewHTMLView(widget.WithOnLink(p.h.htmlLink), widget.WithOnScroll(func(at int) { p.top = at }))
		hv.SetOnRefused(p.h.resourceRefused)
		p.view = &notePageView{HTMLView: hv, page: p}
		p.giveNotice()
	}
	return p.view
}

// Shown makes the page the editor's Rendered view for a note: a Markdown page, on a terminal. Any
// other page keeps what documentFor gave it (a table file its table, the rest their text), and a
// window's editor its own. A note shown while the page is drawn renders at once.
func (p *cellNotePage) Shown(pg Page) {
	if pg.Kind != kind.Markdown || !p.onATerminal() {
		p.shown = fileRef{}
		return
	}
	p.shown = pg.Ref
	v := p.viewOf()
	p.images(pg)
	setDocumentView(p.h.editor, v)
	switch h := p.h; {
	case h.docSwitch != nil:
		h.docSwitch(true)
	default:
		if ds, ok := h.editor.(documentSwitch); ok {
			ds.SetRenderedDocument(true)
		}
	}
	if p.isShown() {
		p.render(pg.Text, true)
	}
}

// Edited renders the note again once typing rests (pagePaused), keeping the reader's place.
func (p *cellNotePage) Edited(pg Page) {
	if pg.Ref == p.shown && p.shown.path != "" && p.isShown() {
		p.render(pg.Text, false)
	}
}

// hidden maps the page's place back to the text: when the reader scrolled the page of the note
// still shown to another block, the cursor goes to that block's first byte, so Raw opens where they
// were reading. Unscrolled, the cursor stays where it was.
func (p *cellNotePage) hidden() {
	h := p.h
	if p.top < 0 || p.top == p.shownTop || !p.onATerminal() || h.file == nil || h.file.ws != p.shown.ws || h.file.path != p.shown.path {
		return
	}
	text := h.core.Value()
	h.core.SetCursorPosition(cursorAt(text, min(p.top, len(text))))
	p.shownTop = p.top
}

// images points the page's images at the note's folder (resourcesFor), only when that changes: a
// new resolver drops the page's images.
func (p *cellNotePage) images(pg Page) {
	root, base := p.h.resourcesFor(pg.Ref.ws, pg.Ref.path)
	if key := root + "\x00" + base; !p.dirSet || key != p.dirKey {
		p.dirKey, p.dirSet = key, true
		if p.h.diskPath() != "" {
			p.view.SetImageResolver(widget.DirResources(root, base))
		} else {
			p.view.SetImageResolver(nil)
		}
	}
}

// render exports text as the page off the loop, as the HTML pane does, and shows it on the loop
// unless a later render came first; toCursor brings the cursor's block to the top.
func (p *cellNotePage) render(text []byte, toCursor bool) {
	h := p.h
	tk := h.ticket(&p.gen, TicketLocal)
	source, theme := append([]byte(nil), text...), h.exportTheme()
	options := export.Options{SourceSpans: true}
	if full := h.diskPath(); full != "" {
		options.Base, options.Document = filepath.Dir(full), full
	}
	do(h, func(context.Context) answerOf[[]byte] {
		return answered(export.RenderWith(source, export.HTML, theme, options))
	}, func(a answerOf[[]byte]) {
		if !tk.Current() || p.view == nil {
			return
		}
		if a.err != nil {
			h.failed(h.tr("autodoc.notice.htmlpane.html_preview"), a.err)
			return
		}
		p.view.SetHTML(a.v)
		if toCursor {
			p.view.ScrollToSource(h.cursorBytes())
			p.shownTop = p.top // the block the cursor is in: a report is synchronous, or none was due
		}
	})
}
