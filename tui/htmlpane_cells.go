package tui

import (
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/tui/widget/htmlview"
)

// THE HTML PREVIEW IN A TERMINAL — File › Preview HTML on a terminal opens the same pane as a
// window's (htmlpane.go), its HTMLView golib's cell view, the note page's (notepage.go): its
// Mermaid diagrams and Vega-Lite charts drawn in cells, live as the note is edited and following
// the cursor, its links opening as the page's do. Over ssh it needs nothing the host may lack (a
// graphics protocol, a headless browser).
//
// The view is given what the page's is: the Editor's highlighting (its code blocks and charts in
// the theme's syntax colours) and the words for a diagram it doesn't draw, when the pane opens and
// again as the theme or the language changes.

// htmlPaneCells is the pane's part as a feature: it hears the theme and the language change.
type htmlPaneCells struct{ h *Host }

func (*htmlPaneCells) Name() string                         { return "htmlpane-cells" }
func (*htmlPaneCells) State() map[string]any                { return map[string]any{} }
func (*htmlPaneCells) Commands() map[string]feature.Command { return map[string]feature.Command{} }

// ThemeChanged gives an open pane the new theme's highlighting, and its page exported again in the
// new theme.
func (c *htmlPaneCells) ThemeChanged() {
	if c.h.htmlPaneShown() {
		c.h.paneLooks()
		c.h.renderHTMLPane()
	}
}

// LanguageChanged gives an open pane the notice in the UI's language now.
func (c *htmlPaneCells) LanguageChanged() {
	if c.h.htmlPaneShown() {
		c.h.paneLooks()
	}
}

// paneLooks gives the pane's view, on a terminal, the Editor's highlighting and the notice for a
// diagram it doesn't draw, as the note page's view has them. A window's pane draws natively, and is
// given neither.
func (h *Host) paneLooks() {
	if h.native() || h.p == nil {
		return
	}
	v, ok := h.htmlView()
	if !ok {
		return
	}
	htmlview.WithSyntax(h.p.SourceLanguages(), h.p.SyntaxStyles())(v)
	// the page's own words, a chart's and a diagram's (pageNotice): a note's page, a card's and the
	// pane say the same
	htmlview.WithDiagramNotice(h.pageNotice)(v)
}
