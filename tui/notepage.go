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
// on a terminal backend, in either build (a GUI build run with --ui too), a Markdown note's Rendered
// view is the editor itself drawing the note through golib's Markdown cell renderer, edited in place
// as in the window: markup hidden, tables boxed, code highlighted in its language, Mermaid and
// Vega-Lite fences drawn, images where the terminal draws them, frontmatter and comments hidden. The
// block typed in shows its source. A window keeps its own (native) path: this does nothing there.
// The view is the window's rule (rendered_gui.go): Rendered at every start, one choice for notes and
// tables, Ctrl+T switching it with the cursor kept.
//
// AN HTML FILE OPENED RAW keeps a page: its own bytes laid out in cells by golib's HTMLView, the
// editor's document view, read-only (its first editing key opens the text and edits, HandleEvent),
// its links opening as the pane's do (docs/decisions/editor.md#html-files-open-raw-or-simplified).

// cellNotePage is the feature: a note's renderer on the terminal's editor, and one page view, the
// editor's document view while an HTML file is shown.
type cellNotePage struct {
	h      *Host
	view   *notePageView
	gen    uint64 // numbers the renders; a later one supersedes an earlier
	shown  fileRef
	html   bool   // the page shown is a raw HTML file's own bytes, not a note exported
	dirKey string // the folder the page's images load from, as last given to the view
	dirSet bool
	// top is the source byte of the block at the page's top, as it last reported it (-1 none);
	// shownTop is top as the page was scrolled to the cursor: a page hidden at another top was
	// scrolled by the reader, and Raw's cursor follows it there (hidden).
	top, shownTop int
	// toCursor is set while the page scrolls itself to the cursor (toCursorBlock): the breadcrumb
	// is the cursor's then, and only a reader's scroll moves it to the page's top
	toCursor bool
	// md draws a note's Rendered view in the editor (renderNote); imgKey is the folder its images
	// load from, as last given to the editor
	md     *widget.MarkdownCellRenderer
	imgKey string
	imgSet bool
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

// HandleEvent reads the page by golib's HTMLView, whose keys follow the editor's keyset (the editor
// gives its page its keyset): in every keyset the keys that never type (the arrows, PgUp/PgDn,
// Home/End, Ctrl+C; Left/Right pan a diagram wider than the page, Alt+Down/Alt+Up go by block and
// Alt+Shift+Down/Up by heading), and under Vim the printing ones too (j/k, g/G, y, h/l, ] [, } {).
// It reads the mouse and its links too; a right press opens the editor's menu there, as over the text
// (the window's Rendered view has it too). Under Vim, Space is the app's leader (SPC) here as over the
// text. An editing key, any other the page doesn't read (i, a, o, x, ':', Esc, typing: under Text
// every printing key, Space too), opens the text at the reader's place and goes on to the editor as
// that same key: Rendered reads as in the window, and the first editing key edits, as there. A
// chord (Ctrl, Alt) the page doesn't read stays the app's.
func (v *notePageView) HandleEvent(ev tuicore.Event) bool {
	k, isKey := ev.(tuicore.KeyEvent)
	if isKey && k.Code == ' ' && k.Mods == 0 && v.page.vimKeys() {
		return false // Vim's leader, as over the text
	}
	if v.HTMLView.HandleEvent(ev) {
		return true
	}
	if m, ok := ev.(tuicore.MouseEvent); ok && m.Kind == tuicore.MousePress && m.Button == tuicore.MouseRight {
		return v.page.menu(v.inEditor(tuicore.Point{X: m.X, Y: m.Y}))
	}
	if !isKey || k.Kind != tuicore.KeyPress {
		return false
	}
	if v.ReadsKey(k) {
		return true // a key the page reads but can't act on now (a copy with nothing selected): still the page's
	}
	if editingKey(k) {
		return v.page.edit(k)
	}
	return false
}

// vimKeys reports whether the editor reads keys by Vim's keyset: Space is then the app's leader.
func (p *cellNotePage) vimKeys() bool {
	e, ok := p.h.editor.(*widget.Editor)
	return !ok || e.Core().Keyset() == widget.KeysetVim
}

// editingKey reports whether k, a key the page doesn't read, edits: Esc, or a key that types (its
// text, with no modifier but Shift).
func editingKey(k tuicore.KeyEvent) bool {
	return k.Code == tuicore.KeyEscape || k.Text != "" && k.Mods&^tuicore.ModShift == 0
}

// inEditor is at, a point in the page's frame, in the editor's: the page lies under the editor's
// Raw | Rendered row when it shows one, so the two frames differ by where the page was placed.
func (v *notePageView) inEditor(at tuicore.Point) tuicore.Point {
	e, ok := v.page.h.editor.(*widget.Editor)
	pc := v.Context()
	if !ok || pc == nil || e.Context() == nil {
		return at
	}
	// resolved from the editor's context, the page's rect is in the editor's frame
	page, ok := e.Context().ResolveAnchor(pc.NodeAnchor())
	if !ok {
		return at
	}
	return tuicore.Point{X: at.X + page.X, Y: at.Y + page.Y}
}

// menu opens the editor's menu at at, in the editor's frame.
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
func (p *cellNotePage) notice(kind string) string  { return p.h.pageNotice(kind) }
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
// page brings the cursor's block to its top, unless it is on screen already (a find's mark put it
// there), and Raw opens there.
func (p *cellNotePage) Moved(_, at int) {
	if !p.shownHere() {
		return
	}
	if from, to, _ := p.onScreen(len(p.h.core.Value())); at < from || at >= to {
		p.toCursorBlock(at)
	}
	p.shownTop = p.top
}

// toCursorBlock scrolls the page to the cursor's block, at source byte at: the page's own scroll,
// which leaves the breadcrumb at the cursor.
func (p *cellNotePage) toCursorBlock(at int) {
	p.toCursor = true
	p.view.ScrollToSource(at)
	p.toCursor = false
}

// cursorToMark puts the editor's cursor at the start of the block holding the page's i-th mark:
// where Raw opens, and where a script sees the find go.
func (p *cellNotePage) cursorToMark(i int) {
	l, ok := p.view.BoundLayout().(widget.HTMLLayoutMarks)
	if !ok {
		return
	}
	y, ok := l.MarkY(i)
	if !ok {
		return
	}
	if src, _ := p.view.BoundLayout().BlockAt(y); src >= 0 {
		text := p.h.core.Value()
		p.h.core.SetCursorPosition(cursorAt(text, min(src, len(text))))
	}
}

// toText opens the note's text at the reader's place, as an editing key does: for a find the page
// can't show. False when there is no terminal editor.
func (p *cellNotePage) toText() bool {
	e, ok := p.h.editor.(*widget.Editor)
	if ok {
		e.SetView(widget.ViewRaw)
	}
	return ok
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
func (p *cellNotePage) LanguageChanged() { p.giveNotice(); p.giveSyntax() }

// ThemeChanged re-gives the page the highlighting its Editor has: a theme switch reloads the
// layout and tells every ThemeSink; the page's code blocks lay out again in the new theme's
// syntax colours, the one thing on the page not token-painted at draw time.
func (p *cellNotePage) ThemeChanged() { p.giveSyntax() }

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

// giveSyntax gives the page the highlighting its Editor has — the same catalog and the same
// SyntaxStyles — when a note is shown (with the program attached, whenever the page was made), as
// the language changes, and as the theme changes (a switch reloads the layout and tells every
// ThemeSink). A page made before the host has a Program gets nothing, until one of these calls
// comes with one.
func (p *cellNotePage) giveSyntax() {
	if p.view == nil || p.h.p == nil {
		return
	}
	widget.WithSyntax(p.h.p.SourceLanguages(), p.h.p.SyntaxStyles())(p.view.HTMLView)
}

// diagramNoticeID is what a diagram the terminal doesn't draw shows: "⟨kind⟩ diagram: not drawn in
// the terminal (Raw shows its source)"; chartNoticeID a vega-lite chart's, one golib's subset leaves
// out: "vega-lite chart: not drawn in the terminal (Raw shows its source)".
const (
	diagramNoticeID = "autodoc.text.rendered_tui.kind_diagram_not_drawn_in_the_terminal"
	chartNoticeID   = "autodoc.text.rendered_tui.vega_lite_chart_not_drawn_in_the_terminal"
)

// pageNotice is what a terminal's page shows for a diagram or chart it doesn't draw, of kind (a
// Mermaid kind, or golib's "vega-lite"), in the UI's language: a note's page and a card's say the
// same.
func (h *Host) pageNotice(kind string) string {
	if kind == "vega-lite" {
		return h.tr(chartNoticeID)
	}
	return h.trf(diagramNoticeID, kind)
}

// viewOf is the page view, made on first use: its links open as the pane's do, and a diagram it
// doesn't draw is said in the UI's language.
func (p *cellNotePage) viewOf() *notePageView {
	if p.view == nil {
		// the reader's place: Raw opens there, and the breadcrumb follows it
		scrolled := func(at int) {
			p.top = at
			if !p.toCursor {
				p.h.pagePlace(at)
			}
		}
		hv := widget.NewHTMLView(widget.WithOnLink(p.h.htmlLink), widget.WithOnScroll(scrolled))
		hv.SetOnRefused(p.h.resourceRefused)
		p.view = &notePageView{HTMLView: hv, page: p}
		p.giveNotice()
		p.giveSyntax()
	}
	return p.view
}

// Shown draws a note, a Markdown page, through the editor's own renderer on a terminal, and makes
// the page the editor's Rendered view for an HTML file opened Raw, its own page
// (docs/decisions/editor.md#html-files-open-raw-or-simplified). Any other page keeps what
// documentFor gave it (a table file its table, the rest their text), with no renderer, and a
// window's editor its own. A page shown while the page view is drawn renders at once.
func (p *cellNotePage) Shown(pg Page) {
	html := p.h.file.open && p.h.file.raw && pg.Ref.path != ""
	if !p.onATerminal() {
		p.shown, p.html = fileRef{}, false
		p.noRenderer() // a terminal's editor turned native (a test's flip) keeps no note's renderer
		return
	}
	if pg.Kind == kind.Markdown && !html {
		p.shown, p.html = fileRef{}, false
		p.renderNote(pg)
		return
	}
	p.noRenderer()
	if !html {
		p.shown, p.html = fileRef{}, false
		return
	}
	p.shown, p.html = pg.Ref, html
	v := p.viewOf()
	p.giveSyntax() // the page may have been made before the program attached
	p.images(pg)
	setDocumentView(p.h.editor, v)
	p.h.setRenderedDocument(true)
	if p.isShown() {
		p.render(pg.Text, true)
	}
}

// renderNote draws note pg's Rendered view in the editor: its Markdown cell renderer, highlighting
// code in the program's languages, its images loading from the note's folder, and no page over it.
func (p *cellNotePage) renderNote(pg Page) {
	e := p.h.editor.(*widget.Editor) // on a terminal (onATerminal)
	if p.md == nil {
		p.md = &widget.MarkdownCellRenderer{MarkdownBlocks: widget.MarkdownBlocks{HideMetadata: true}}
	}
	if p.h.p != nil {
		p.md.Languages = p.h.p.SourceLanguages() // the program may have attached since
	}
	if e.Renderer() != p.md {
		e.SetRenderer(p.md)
	}
	root, base := p.h.resourcesFor(pg.Ref.ws, pg.Ref.path)
	if key := root + "\x00" + base; !p.imgSet || key != p.imgKey {
		p.imgKey, p.imgSet = key, true
		if p.h.diskPath() != "" {
			e.SetImages(widget.DirResources(root, base))
		} else {
			e.SetImages(nil)
		}
	}
	p.h.setRenderedDocument(false)
}

// noRenderer takes a note's renderer off the editor: another page is shown, its text, a table or
// an HTML file's page.
func (p *cellNotePage) noRenderer() {
	if e, ok := p.h.editor.(*widget.Editor); ok && p.md != nil && e.Renderer() == p.md {
		e.SetRenderer(nil)
	}
}

// Edited lays an HTML file's page out again once typing rests (pagePaused), keeping the
// reader's place.
func (p *cellNotePage) Edited(pg Page) {
	if pg.Ref == p.shown && p.shown.path != "" && p.isShown() {
		p.render(pg.Text, false)
	}
}

// hidden maps the page's place back to the text: when the reader scrolled the page of the file
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
	if p.html {
		// a raw HTML file is its own page: its bytes as they are (the GUI's page reads them so too);
		// they carry no source spans, so the page keeps its place rather than finding the cursor's
		p.view.SetHTML(append([]byte(nil), text...))
		return
	}
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
			p.toCursorBlock(h.cursorBytes())
			p.shownTop = p.top // the block the cursor is in: a report is synchronous, or none was due
			h.findRecount()    // shown: a find on counts and marks what the page shows
		}
	})
}
