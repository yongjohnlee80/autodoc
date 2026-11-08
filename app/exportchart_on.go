//go:build gui

package app

import (
	"context"

	"github.com/yongjohnlee80/golib/gui"
	"github.com/yongjohnlee80/golib/gui/vegalite"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// chartSVG draws a Vega-Lite spec as the app's Rendered view does, with golib's gui/vegalite, on a
// gui.SVGCanvas, its labels measured by the window's shaper: inline SVG in the export's palette
// (docs/decisions/editor.md#charts-drawn-as-svg). A build without the GUI draws it with
// chart/vegalite (exportchart_off.go). A chart vegalite cannot lay out answers its error, in
// vegalite's words (a chart outside its subset says which part): the export captions the spec
// with it.
var chartSVG = func(spec []byte, theme string) (string, error) {
	bg, fg, muted, border := export.Colors(theme)
	m, release := gui.AcquireMeasurer(1)
	defer release()
	laid, err := vegalite.Lay(context.Background(), string(spec), chartWidth, vegalite.Theme{
		Background: nrgba(bg), Text: nrgba(fg), Axis: nrgba(muted), Grid: nrgba(border),
	}, m)
	if err != nil {
		return "", err
	}
	c := gui.NewSVGCanvas(laid.Size(), nil)
	laid.Paint(c)
	return string(c.Bytes()), nil
}
