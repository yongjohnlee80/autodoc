package tui

import (
	"context"
	"path/filepath"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/kind"
)

// A NOTE'S RENDERED VIEW IN A TERMINAL (docs/decisions/editor.md#a-terminal-notes-rendered-view):
// on a terminal backend, in either build (a GUI build run with --ui too), Raw is the note's text,
// Rendered the page, as in the window. A window keeps its own (native) path: this does nothing there.
// The view is the window's rule (rendered_gui.go): Rendered at every start, one choice for notes and
// tables, Ctrl+T switching it. The window's Rendered Markdown stays editable; the page is read-only,
// so its first editing key opens the text and edits (HandleEvent). The page is the note exported in
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
	rows int // the height it was last laid out at: what is on screen (cellNotePage.onScreen)
}

// Layout lays the page out, keeping its height.
func (v *notePageView) Layout(c tuicore.Constraints) tuicore.Size {
	sz := v.HTMLView.Layout(c)
	v.rows = sz.H
	return sz
}

// ShowDocument renders text, the note, as its page: the editor calls it as the view is shown.
func (v *notePageView) ShowDocument(text []byte) { v.page.render(text, true) }

// SetVisible shows or hides the page; hidden (Ctrl+T to Raw), the cursor goes where the reader
// scrolled to.
func (v *notePageView) SetVisible(on bool) {
	v.HTMLView.SetVisible(on)
	if !on {
		v.page.hidden()
		v.page.h.findRecount() // the text drawn again: a find on counts its source
	}
}

// HandleEvent reads the page by its own keys (the arrows, j/k, PgUp/PgDn, g/G, y and Ctrl+C), the
// mouse and its links; a right press opens the editor's menu there, as over the text (the window's
// Rendered view has it too). Space is the app's leader (SPC) here as over the text; PgDn pages. An editing
// key, any other the page doesn't read (i, a, o, x, ':', Esc, typing), opens the text at the
// reader's place and goes on to the editor as that same key: Rendered reads as in the window, and the
// first editing key edits, as there. A chord (Ctrl, Alt) stays the app's.
func (v *notePageView) HandleEvent(ev tuicore.Event) bool {
	k, isKey := ev.(tuicore.KeyEvent)
	if isKey && k.Code == ' ' && k.Mods == 0 {
		return false
	}
	if v.HTMLView.HandleEvent(ev) {
		return true
	}
	if m, ok := ev.(tuicore.MouseEvent); ok && m.Kind == tuicore.MousePress && m.Button == tuicore.MouseRight {
		return v.page.menu(tuicore.Point{X: m.X, Y: m.Y})
	}
	if !isKey || k.Kind != tuicore.KeyPress {
		return false
	}
	if _, copies := v.Context().KeyCommand(k, widget.HTMLViewCopy); copies {
		return true // Copy (y) with nothing selected copies nothing: a reading key, the page's still
	}
	if editingKey(k) {
		return v.page.edit(k)
	}
	return false
}

// editingKey reports whether k, a key the page doesn't read, edits: Esc, or a key that types (its
// text, with no modifier but Shift).
func editingKey(k tuicore.KeyEvent) bool {
	return k.Code == tuicore.KeyEscape || k.Text != "" && k.Mods&^tuicore.ModShift == 0
}

// menu opens the editor's menu at at, in the page's frame: the page lies over the editor's whole
// area, so its frame is the editor's.
func (p *cellNotePage) menu(at tuicore.Point) bool {
	e, ok := p.h.editor.(*widget.Editor)
	return ok && e.ContextMenu().OpenAt(at)
}

// edit opens the text at the reader's place (hidden maps it) and gives it k, the editing key that
// left the page.
func (p *cellNotePage) edit(k tuicore.KeyEvent) bool {
	e, ok := p.h.editor.(*widget.Editor)
	if !ok {
		return false
	}
	e.SetView(widget.ViewRaw)
	return e.HandleEvent(k)
}

var _ widget.DocumentView = (*notePageView)(nil)

func newCellNotePage(h *Host) *cellNotePage { return &cellNotePage{h: h, top: -1, shownTop: -1} }

func (*cellNotePage) Name() string                 { return "notepage" }
func (*cellNotePage) State() map[string]any        { return map[string]any{} }
func (*cellNotePage) Commands() map[string]Command { return map[string]Command{} }
func (*cellNotePage) Reread(Page)                  {}
func (*cellNotePage) Closed()                      {}
func (p *cellNotePage) notice(kind string) string  { return p.h.trf(diagramNoticeID, kind) }
func (p *cellNotePage) isShown() bool              { return p.view != nil && p.view.Visible() }

// onScreen is the source bytes the shown page has on screen, from its top block's start to the
// start of the first block below it (the note's end when none is); false when no page is shown.
func (p *cellNotePage) onScreen(n int) (from, to int, ok bool) {
	if !p.shownHere() {
		return 0, 0, false
	}
	l := p.view.BoundLayout()
	from, to = p.top, n
	if y := p.view.ScrollY() + float32(p.view.rows); y < l.Height() {
		if b, _ := l.BlockAt(y); b > from {
			to = b
		}
	}
	return min(max(from, 0), n), min(to, n), true
}

// Moved is the cursor landing elsewhere while the page is shown, a jump's (Ctrl+O, Ctrl+I): the
// page brings the cursor's block to its top, and Raw opens there.
func (p *cellNotePage) Moved(_, at int) {
	if !p.shownHere() {
		return
	}
	p.view.ScrollToSource(at)
	p.shownTop = p.top
}

// shownHere reports whether a note's page is shown on this terminal.
func (p *cellNotePage) shownHere() bool { return p.isShown() && p.onATerminal() }

// firstMarkFromTop is the first of the page's n marks at or after its top; the first when none is.
func (p *cellNotePage) firstMarkFromTop(n int) int {
	l, ok := p.view.BoundLayout().(widget.HTMLLayoutMarks)
	if !ok {
		return 0
	}
	for i := range n {
		if y, ok := l.MarkY(i); ok && y >= p.view.ScrollY() {
			return i
		}
	}
	return 0
}

// currentMark makes the page's i-th mark the current one, where the page is: no scroll.
func (p *cellNotePage) currentMark(i int) {
	if l, ok := p.view.BoundLayout().(widget.HTMLLayoutMarks); ok {
		l.SetCurrentMark(i)
		p.view.MarkDirty()
	}
}

// notePage is the host's note page, nil when it has none.
func (h *Host) notePage() *cellNotePage {
	for _, f := range h.reg.features {
		if p, ok := f.(*cellNotePage); ok {
			return p
		}
	}
	return nil
}

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
		// the reader's place: Raw opens there, and the breadcrumb follows it
		scrolled := func(at int) { p.top = at; p.h.pagePlace(at) }
		hv := widget.NewHTMLView(widget.WithOnLink(p.h.htmlLink), widget.WithOnScroll(scrolled))
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
			h.findRecount()    // shown: a find on counts and marks what the page shows
		}
	})
}
