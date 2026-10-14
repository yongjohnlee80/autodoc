//go:build gui

package tui

import (
	"context"
	"image/color"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
)

// chartNotice is the fallback for the charts golib does not draw (its Vega-Lite subset declines
// them, or one is over a limit): a short notice in place of the chart, not its spec as code, which
// can run to hundreds of lines. Anything else it declines (a Mermaid diagram type), and a chart
// that is not JSON, stay code.
type chartNotice struct{}

// nativeDiagrams is golib's native chain, as AutoDoc draws with it: a chart it declines is the notice.
func nativeDiagrams() guiwidget.Diagrammer { return guiwidget.Diagrams(chartNotice{}) }

const chartNoticeText = "This Vega-Lite chart isn't supported in AutoDoc yet. Its source is in the Raw view, or in its .chart.json file."

func (chartNotice) Diagram(_ context.Context, req guiwidget.DiagramRequest, _ func()) guiwidget.DiagramAnswer {
	if req.Lang != "vega-lite" && req.Lang != "vegalite" {
		return guiwidget.DiagramAnswer{State: guiwidget.Declined}
	}
	th := req.Theme
	f := th.Prose
	f.Size = max(f.Size*0.9, 10)
	f.Italic = true
	pad := f.Size
	m, release := gui.AcquireMeasurer(req.Scale)
	_, sz := m.Wrap(chartNoticeText, f, max(req.Width-2*pad, f.Size))
	release()
	n := noticeView{text: chartNoticeText, font: f, pad: pad, size: gui.Size{W: req.Width, H: sz.H + 2*pad},
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
