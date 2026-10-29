//go:build gui

package tui

import (
	"context"
	"image/color"
	"sync"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
)

// chartNotice is the fallback for the charts golib does not draw (its Vega-Lite subset declines
// them, or one is over a limit): a short notice in place of the chart, not its spec as code, which
// can run to hundreds of lines. It says why in golib's words, the error golib declined the chart
// with (DiagramRequest.Declined), as an export's caption does. A Mermaid diagram golib declined
// with a reason (a kind or syntax outside its subset) is a notice of that reason the same way. A
// syntax error never reaches it (golib draws the error in place); anything else declined with no
// reason, and a chart that is not JSON, stay code.
//
// Its sentences are the UI's, translated whole in trf's language (the host's trf), golib's error
// filled in as golib wrote it; with trf nil, English.
type chartNotice struct {
	trf func(id string, args ...any) string
}

// nativeDiagrams is golib's native chain, as AutoDoc draws with it: a chart it declines is the
// notice, in trf's language.
func nativeDiagrams(trf func(id string, args ...any) string) guiwidget.Diagrammer {
	return guiwidget.Diagrams(chartNotice{trf})
}

// The notice's sentences: a chart golib declined with no reason, a chart it declined with one, and
// a Mermaid diagram it declined with one (%1, golib's error).
const (
	chartNoticeUnsupported = "autodoc.notice.chartnotice.this_vega_lite_chart_isn_t_supported_in_autodoc"
	chartNoticeNotDrawn    = "autodoc.notice.chartnotice.chart_not_drawn_s_its_source_is_in_the_raw_view"
	diagramNoticeNotDrawn  = "autodoc.notice.chartnotice.diagram_not_drawn_s_its_source_is_in_the_raw_vie"
)

// text is the notice for req, and whether it has one: a chart's, with golib's reason when it gave
// one; a Mermaid diagram's when golib gave a reason.
func (c chartNotice) text(req guiwidget.DiagramRequest) (string, bool) {
	trf := c.trf
	if trf == nil {
		trf = inEnglish{}.Trf
	}
	switch {
	case (req.Lang == "vega-lite" || req.Lang == "vegalite") && req.Declined == nil:
		return trf(chartNoticeUnsupported), true
	case req.Lang == "vega-lite" || req.Lang == "vegalite":
		return trf(chartNoticeNotDrawn, req.Declined.Error()), true
	case req.Lang == "mermaid" && req.Declined != nil:
		return trf(diagramNoticeNotDrawn, req.Declined.Error()), true
	}
	return "", false
}

// noticeShapers are the notices' shapers, one per scale, made once and kept: Diagram runs on the UI
// loop, in a layout (golib's diagrams hand it a declined chart synchronously), so it never takes a
// pooled measurer, which waits while the diagram workers hold them all (gui.AcquireMeasurer is for
// workers).
var noticeShapers struct {
	mu sync.Mutex
	m  map[float32]*gui.TextShaper
}

// noticeWrap wraps text at scale with the notices' shaper there, made the first time and kept. The
// lock covers the wrap too: a shaper is not safe for concurrent use.
func noticeWrap(scale float32, text string, f gui.Font, width float32) ([]string, gui.Size) {
	noticeShapers.mu.Lock()
	defer noticeShapers.mu.Unlock()
	t := noticeShapers.m[scale]
	if t == nil {
		if noticeShapers.m == nil {
			noticeShapers.m = map[float32]*gui.TextShaper{}
		}
		t = gui.NewTextShaper(scale)
		noticeShapers.m[scale] = t
	}
	return t.Wrap(text, f, width)
}

func (c chartNotice) Diagram(_ context.Context, req guiwidget.DiagramRequest, _ func()) guiwidget.DiagramAnswer {
	text, ok := c.text(req)
	if !ok {
		return guiwidget.DiagramAnswer{State: guiwidget.Declined}
	}
	th := req.Theme
	f := th.Prose
	f.Size = max(f.Size*0.9, 10)
	f.Italic = true
	pad := f.Size
	_, sz := noticeWrap(req.Scale, text, f, max(req.Width-2*pad, f.Size))
	n := noticeView{text: text, font: f, pad: pad, size: gui.Size{W: req.Width, H: sz.H + 2*pad},
		ink: orColor(th.Muted, th.Text), ground: orColor(th.CodeBackground, th.Background)}
	return guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: n, Size: n.size}
}

// noticeView is the notice: its text, wrapped, in a rounded box on the code blocks' ground.
type noticeView struct {
	text   string
	font   gui.Font
	pad    float32
	size   gui.Size
	ink    color.NRGBA
	ground color.NRGBA
}

func (v noticeView) Paint(c gui.Canvas) {
	box := gui.Rect{W: v.size.W, H: v.size.H}
	c.FillRRect(box, v.pad/2, gui.Solid(v.ground))
	c.StrokeRRect(box, v.pad/2, 1, gui.Solid(v.ink))
	t := c.Text().Layout(v.text, v.font, max(v.size.W-2*v.pad, v.font.Size))
	c.DrawText(t, gui.Pt(v.pad, v.pad), gui.Solid(v.ink))
}

// orColor is c, or d when c is unset.
func orColor(c, d color.NRGBA) color.NRGBA {
	if c.A == 0 {
		return d
	}
	return c
}
