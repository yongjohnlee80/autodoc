package tui

import (
	"context"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
	tuimermaid "github.com/yongjohnlee80/golib/tui/mermaid"
)

// cellDiagram reports whether golib's Mermaid draws src in a terminal's characters (golib
// tui/mermaid, as a cell HTMLView draws a page's diagrams): nil when it does; the reason when it
// does not, a malformed source, a diagram type it does not draw or one over a limit. A terminal's
// native engine (File › Preview option), and what draws a diagram where a terminal shows no image.
// Called off the loop.
func cellDiagram(src string) error {
	d, err := pm.Parse(src)
	if err != nil {
		return err
	}
	_, err = tuimermaid.Lay(context.Background(), d, feature.CellMeasure{})
	return err
}
