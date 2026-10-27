//go:build gui

package app

import (
	"context"

	"github.com/yongjohnlee80/golib/gui"
	"github.com/yongjohnlee80/golib/gui/mermaid"
	"github.com/yongjohnlee80/golib/gui/vegalite"
)

// checkWidth is the width --check-diagram lays a diagram out at: an exported chart's (chartWidth).
const checkWidth = chartWidth

func init() {
	// golib's own Lay for each language, as the Rendered view's engine calls it: what it draws, and
	// the error of what it does not, are the GUI's
	layDiagram = func(lang, src string) error {
		m, release := gui.AcquireMeasurer(1)
		defer release()
		var err error
		switch lang {
		case "mermaid":
			_, err = mermaid.Lay(context.Background(), src, checkWidth, mermaid.Theme{Font: gui.Font{Size: 14}}, m)
		default:
			_, err = vegalite.Lay(context.Background(), src, checkWidth, vegalite.Theme{}, m)
		}
		return err
	}
}
