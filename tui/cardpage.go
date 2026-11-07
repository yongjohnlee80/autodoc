package tui

import (
	"context"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// A CARD'S PAGE IN A TERMINAL (docs/decisions/editor.md#lua-cards): a
// card's text is Markdown, drawn Rendered. A window's Editor draws it natively; a terminal's draws
// this page, golib's HTMLView over the text exported in process, as a note's page is (notepage.go).
// So a vega-lite fence on a card is a chart in either (in cells: bars, Braille plots or a table), a
// spec outside golib's subset the notice, never its JSON; and autodoc.ui.charts() answers true
// wherever a card is shown. The page is the card editor's document view, given it when a card first
// sets its text on a terminal, and drawn again each time the text changes, the reader's place kept.

// cardPage is the feature: the card's page, made on first use.
type cardPage struct {
	h    *Host
	view *cardPageView
	gen  uint64 // numbers the renders; a later one supersedes an earlier
}

// cardPageView is the page as the card editor's document view: shown, it renders the card's text,
// which is what the editor hands it.
type cardPageView struct {
	*widget.HTMLView
	page *cardPage
}

// ShowDocument renders text, the card's: the editor calls it as the view is shown.
func (v *cardPageView) ShowDocument(text []byte) { v.page.render(text) }

var _ widget.DocumentView = (*cardPageView)(nil)

func newCardPage(h *Host) *cardPage { return &cardPage{h: h} }

func (*cardPage) Name() string                 { return "cardpage" }
func (*cardPage) State() map[string]any        { return map[string]any{} }
func (*cardPage) Commands() map[string]Command { return map[string]Command{} }

// LanguageChanged and ThemeChanged give the page the words and the code colours of now, as the
// note's page does: its notice, and its charts' series (the page's code colours).
func (p *cardPage) LanguageChanged() { p.give() }
func (p *cardPage) ThemeChanged()    { p.give() }

// cardPage is the host's card page, nil when it has none.
func (h *Host) cardPage() *cardPage {
	for _, f := range h.reg.features {
		if p, ok := f.(*cardPage); ok {
			return p
		}
	}
	return nil
}

// editor is the card's terminal editor; false in a window, whose Editor draws the card itself.
func (p *cardPage) editor() (*widget.Editor, bool) {
	if p.h.p == nil || p.h.native() {
		return nil, false
	}
	return tuidecl.FindAs[*widget.Editor](p.h.p, luaCardView)
}

// textSet hears the card's text set (a new card, card:set): on a terminal the card's editor gets
// the page, Rendered, and a page shown draws the text again.
func (p *cardPage) textSet(text string) {
	e, ok := p.editor()
	if !ok {
		return
	}
	v := p.viewOf()
	e.SetDocumentView(v) // the same view again changes nothing
	e.SetRenderedDocument(true)
	if v.Visible() {
		p.render([]byte(text))
	}
}

// viewOf is the page view, made on first use: its links open as the pane's do, and a diagram or
// chart it doesn't draw is said in the UI's language.
func (p *cardPage) viewOf() *cardPageView {
	if p.view == nil {
		p.view = &cardPageView{HTMLView: widget.NewHTMLView(widget.WithOnLink(p.h.htmlLink)), page: p}
		p.give()
	}
	return p.view
}

// give gives the page its notice and its highlighting (the program's catalog and syntax styles,
// once the program is attached).
func (p *cardPage) give() {
	if p.view == nil {
		return
	}
	widget.WithDiagramNotice(p.h.pageNotice)(p.view.HTMLView)
	if p.h.p != nil {
		widget.WithSyntax(p.h.p.SourceLanguages(), p.h.p.SyntaxStyles())(p.view.HTMLView)
	}
}

// render exports text as the page off the loop, as the note's page does, and shows it on the loop
// unless a later render came first.
func (p *cardPage) render(text []byte) {
	h := p.h
	tk := h.ticket(&p.gen, TicketLocal)
	source, theme := append([]byte(nil), text...), h.exportTheme()
	do(h, func(context.Context) answerOf[[]byte] {
		return answered(export.RenderWith(source, export.HTML, theme, export.Options{}))
	}, func(a answerOf[[]byte]) {
		if !tk.Current() || p.view == nil {
			return
		}
		if a.err != nil {
			h.failed(h.tr("autodoc.notice.htmlpane.html_preview"), a.err)
			return
		}
		p.view.SetHTML(a.v)
	})
}
