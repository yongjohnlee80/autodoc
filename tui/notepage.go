package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
	"github.com/yongjohnlee80/golib/tui/widget/htmlview"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/tui/feature"
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
	h    *Host
	view *notePageView
	// shown is the HTML file the page shows (none for a note or any other page). Its own bytes carry
	// no source spans, so the page's place maps to no place in the text: Raw opens at the cursor.
	shown  feature.FileRef
	dirKey string // the folder the page's images load from, as last given to the view
	dirSet bool
	// md draws a note's Rendered view in the editor (renderNote); imgKey is the folder its images
	// load from, as last given to the editor
	md     *editor.MarkdownCellRenderer
	imgKey string
	imgSet bool
	// embeds answers md for the note's embedded diagrams, charts and tables (noteembeds.go), the
	// note's file on disk its own
	embeds *termEmbeds
}

// notePageView is the page as the editor's document view: shown, it lays out the HTML file's
// text, which is what the editor hands it.
type notePageView struct {
	*htmlview.HTMLView
	page *cellNotePage
}

// ShowDocument lays text, the HTML file, out as its page: the editor calls it as the view is shown,
// before SetVisible.
func (v *notePageView) ShowDocument(text []byte) { v.page.render(text) }

// SetVisible shows or hides the page. Either way a find on counts what is drawn now: shown (Ctrl+T
// back from Raw), the page's marks; hidden, the text's source.
func (v *notePageView) SetVisible(on bool) {
	v.HTMLView.SetVisible(on)
	v.page.h.findRecount()
}

// HandleEvent reads the page by golib's HTMLView, whose keys follow the editor's keyset (the editor
// gives its page its keyset): in every keyset the keys that never type (the arrows, PgUp/PgDn,
// Home/End, Ctrl+C; Left/Right pan a diagram wider than the page, Alt+Down/Alt+Up go by block and
// Alt+Shift+Down/Up by heading), and under Vim the printing ones too (j/k, g/G, y, h/l, ] [, } {).
// It reads the mouse and its links too; a right press opens the editor's menu there, as over the text
// (the window's Rendered view has it too). Under Vim, Space is the app's leader (SPC) here as over the
// text. An editing key, any other the page doesn't read (i, a, o, x, ':', Esc, typing: under Text
// every printing key, Space too), opens the text at its cursor and goes on to the editor as
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
	e, ok := p.h.editor.(*editor.Editor)
	return !ok || e.Core().Keyset() == editor.KeysetVim
}

// editingKey reports whether k, a key the page doesn't read, edits: Esc, or a key that types (its
// text, with no modifier but Shift).
func editingKey(k tuicore.KeyEvent) bool {
	return k.Code == tuicore.KeyEscape || k.Text != "" && k.Mods&^tuicore.ModShift == 0
}

// inEditor is at, a point in the page's frame, in the editor's: the page lies under the editor's
// Raw | Rendered row when it shows one, so the two frames differ by where the page was placed.
func (v *notePageView) inEditor(at tuicore.Point) tuicore.Point {
	e, ok := v.page.h.editor.(*editor.Editor)
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
	e, ok := p.h.editor.(*editor.Editor)
	return ok && e.ContextMenu().OpenAt(at)
}

// edit opens the text at its cursor and gives it k, the editing key that left the page.
func (p *cellNotePage) edit(k tuicore.KeyEvent) bool {
	e, ok := p.h.editor.(*editor.Editor)
	if !ok {
		return false
	}
	e.SetView(editor.ViewRaw)
	return e.HandleEvent(k)
}

var _ editor.DocumentView = (*notePageView)(nil)

func newCellNotePage(h *Host) *cellNotePage { return &cellNotePage{h: h} }

func (*cellNotePage) Name() string                         { return "notepage" }
func (*cellNotePage) State() map[string]any                { return map[string]any{} }
func (*cellNotePage) Commands() map[string]feature.Command { return map[string]feature.Command{} }
func (*cellNotePage) Reread(feature.Page)                  {}
func (*cellNotePage) Closed()                              {}
func (p *cellNotePage) notice(kind string) string          { return p.h.pageNotice(kind) }
func (p *cellNotePage) isShown() bool                      { return p.view != nil && p.view.Visible() }

// Moved changes nothing: the page has no place in the text to bring the cursor's to.
func (*cellNotePage) Moved(int, int) {}

// toText opens the file's text at its cursor, as an editing key does: for a find the page can't
// show. False when there is no terminal editor.
func (p *cellNotePage) toText() bool {
	e, ok := p.h.editor.(*editor.Editor)
	if ok {
		e.SetView(editor.ViewRaw)
	}
	return ok
}

// shownHere reports whether an HTML file's page is shown on this terminal.
func (p *cellNotePage) shownHere() bool { return p.isShown() && p.onATerminal() }

// firstMarkFromTop is the first of the page's n marks at or after its top; the first when none is.
func (p *cellNotePage) firstMarkFromTop(n int) int {
	l, ok := p.view.BoundLayout().(htmlview.HTMLLayoutMarks)
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
	if l, ok := p.view.BoundLayout().(htmlview.HTMLLayoutMarks); ok {
		l.SetCurrentMark(i)
		p.view.MarkDirty()
	}
}

// notePage is the host's note page, nil when it has none.
func (h *Host) notePage() *cellNotePage {
	for _, f := range h.reg.Features {
		if p, ok := f.(*cellNotePage); ok {
			return p
		}
	}
	return nil
}

// onATerminal reports whether the program draws in a terminal's cells: not a window's native views,
// and the editor the terminal's (preview.go's showDiagram decides the same way).
func (p *cellNotePage) onATerminal() bool {
	_, ok := p.h.editor.(*editor.Editor)
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
	htmlview.WithDiagramNotice(p.notice)(p.view.HTMLView)
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
	htmlview.WithSyntax(p.h.p.SourceLanguages(), p.h.p.SyntaxStyles())(p.view.HTMLView)
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
		hv := htmlview.NewHTMLView(htmlview.WithOnLink(p.h.htmlLink))
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
func (p *cellNotePage) Shown(pg feature.Page) {
	html := p.h.file.open && p.h.file.raw && pg.Ref.Path != ""
	p.shown = feature.FileRef{}
	if !p.onATerminal() {
		p.noRenderer() // a terminal's editor turned native (a test's flip) keeps no note's renderer
		return
	}
	if pg.Kind == kind.Markdown && !html {
		p.renderNote(pg)
		return
	}
	p.noRenderer()
	if !html {
		return
	}
	p.shown = pg.Ref
	v := p.viewOf()
	p.giveSyntax() // the page may have been made before the program attached
	p.images(pg)
	setDocumentView(p.h.editor, v)
	p.h.setRenderedDocument(true)
	if p.isShown() {
		p.render(pg.Text)
	}
}

// renderNote draws note pg's Rendered view in the editor: its Markdown cell renderer, highlighting
// code in the program's languages, its images loading from the note's folder, and no page over it.
func (p *cellNotePage) renderNote(pg feature.Page) {
	e := p.h.editor.(*editor.Editor) // on a terminal (onATerminal)
	if p.md == nil {
		p.md = &editor.MarkdownCellRenderer{MarkdownBlocks: editor.MarkdownBlocks{HideMetadata: true}}
	}
	if p.h.p != nil {
		p.md.Languages = p.h.p.SourceLanguages() // the program may have attached since
	}
	if e.Renderer() != p.md {
		e.SetRenderer(p.md)
	}
	if note := p.h.diskPath(); p.embeds == nil || p.embeds.note != note {
		p.closeEmbeds()
		p.embeds = &termEmbeds{note: note, trf: p.h.trf, follow: p.h.followDir, changed: p.embedsChanged}
	}
	p.md.Embeds = p.embeds.resolve
	// a safeguard for a show that reuses the layout, with no switch: a switch to the note already
	// lays it again (SetImages, SetRenderer), so an embedded file written since is read anew there
	e.RelayRendered()
	root, base := p.h.resourcesFor(pg.Ref.WS, pg.Ref.Path)
	if key := root + "\x00" + base; !p.imgSet || key != p.imgKey {
		p.imgKey, p.imgSet = key, true
		if p.h.diskPath() != "" {
			e.SetImages(htmlview.DirResources(root, base))
		} else {
			e.SetImages(nil)
		}
	}
	p.h.setRenderedDocument(false)
}

// noRenderer takes a note's renderer off the editor: another page is shown, its text, a table or
// an HTML file's page.
func (p *cellNotePage) noRenderer() {
	p.closeEmbeds()
	if e, ok := p.h.editor.(*editor.Editor); ok && p.md != nil && e.Renderer() == p.md {
		e.SetRenderer(nil)
	}
}

// closeEmbeds ends the shown note's follows of its embedded files (noteembeds.go): it is shown no
// more, and a note shown again reads its embeds afresh.
func (p *cellNotePage) closeEmbeds() {
	if p.embeds != nil {
		p.embeds.close()
		p.embeds = nil
	}
}

// embedsChanged lays the note's Rendered view out again when an embedded file changed on disk,
// while the note's renderer is the editor's.
func (p *cellNotePage) embedsChanged() {
	if e, ok := p.h.editor.(*editor.Editor); ok && p.md != nil && e.Renderer() == p.md {
		e.RelayRendered()
	}
}

// Edited lays an HTML file's page out again once typing rests (pagePaused), keeping the
// reader's place.
func (p *cellNotePage) Edited(pg feature.Page) {
	if pg.Ref == p.shown && p.shown.Path != "" && p.isShown() {
		p.render(pg.Text)
	}
}

// images points the page's images at the note's folder (resourcesFor), only when that changes: a
// new resolver drops the page's images.
func (p *cellNotePage) images(pg feature.Page) {
	root, base := p.h.resourcesFor(pg.Ref.WS, pg.Ref.Path)
	if key := root + "\x00" + base; !p.dirSet || key != p.dirKey {
		p.dirKey, p.dirSet = key, true
		if p.h.diskPath() != "" {
			p.view.SetImageResolver(htmlview.DirResources(root, base))
		} else {
			p.view.SetImageResolver(nil)
		}
	}
}

// render lays text, a raw HTML file's own bytes, out as the page, as the GUI's page reads them.
func (p *cellNotePage) render(text []byte) { p.view.SetHTML(append([]byte(nil), text...)) }
