package app

import (
	"image/color"

	"github.com/yongjohnlee80/golib/tui/style"
)

// chartWidth is the width an exported chart is laid out for, in logical pixels: a page's text
// column, which it scales down to fit.
const chartWidth = 640

// nrgba is a palette colour (#rrggbb) as a chart is drawn in it.
func nrgba(hex string) color.NRGBA {
	c, err := style.ParseColor(hex)
	if err != nil {
		return color.NRGBA{A: 0xff}
	}
	r, g, b, _ := c.RGBValues()
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}
}
