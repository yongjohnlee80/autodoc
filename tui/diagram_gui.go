//go:build gui

package tui

import (
	"context"
	"errors"

	"github.com/yongjohnlee80/golib/gui"
	guimermaid "github.com/yongjohnlee80/golib/gui/mermaid"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// nativeDiagramWidth is the width a diagram is laid out at to learn whether golib draws it.
const nativeDiagramWidth = 900

// nativeDiagram reports whether golib's Mermaid draws src natively: nil when it does, or when the
// source is malformed (it draws the error in place, as the window's diagrams do); the reason when
// it does not, a diagram type it does not draw or one over a limit. Called off the loop.
func nativeDiagram(src string) error {
	m, release := gui.AcquireMeasurer(1)
	defer release()
	_, err := guimermaid.Lay(context.Background(), src, nativeDiagramWidth, guimermaid.Theme{}, m)
	var se *pm.SyntaxError
	if err == nil || errors.As(err, &se) {
		return nil
	}
	return err
}
