//go:build gui

package app

import (
	"context"
	"image/color"

	"github.com/yongjohnlee80/golib/gui"
	"github.com/yongjohnlee80/golib/gui/vegalite"
	"github.com/yongjohnlee80/golib/tui/style"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// chartWidth is the width an exported chart is laid out for, in logical pixels: a page's text
// column, which it scales down to fit.
const chartWidth = 640

// chartSVG draws a Vega-Lite spec as the app's Rendered view does, with golib's gui/vegalite, on a
// gui.SVGCanvas: inline SVG in the export's palette (docs/decisions/editor.md#charts-drawn-as-svg).
// A chart vegalite cannot lay out answers its error, in vegalite's words (a chart outside its
// subset says which part): the export captions the spec with it.
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

// nrgba is a palette colour (#rrggbb) as gui draws it.
func nrgba(hex string) color.NRGBA {
	c, err := style.ParseColor(hex)
	if err != nil {
		return color.NRGBA{A: 0xff}
	}
	r, g, b, _ := c.RGBValues()
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}
}
