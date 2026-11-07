//go:build !gui

package app

import (
	"context"

	"github.com/yongjohnlee80/golib/chart/vegalite"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// chartSVG draws a Vega-Lite spec in a build without the GUI, as the GUI build does
// (exportchart_on.go): golib's chart/vegalite lays it out and writes it as inline SVG in the
// export's palette (docs/decisions/editor.md#charts-drawn-as-svg). With no fonts loaded here, its
// labels are measured by Helvetica's advances; the browser draws them in its own sans-serif. A
// chart chart/vegalite cannot lay out answers its error, in its words: the export captions the
// spec with it.
var chartSVG = func(spec []byte, theme string) (string, error) {
	bg, fg, muted, border := export.Colors(theme)
	laid, err := vegalite.Lay(context.Background(), string(spec), chartWidth, vegalite.Theme{
		Background: nrgba(bg), Text: nrgba(fg), Axis: nrgba(muted), Grid: nrgba(border),
	}, nil)
	if err != nil {
		return "", err
	}
	return string(laid.SVG()), nil
}
