//go:build !gui

package app

import (
	"context"
	"errors"

	"github.com/yongjohnlee80/autodoc/tui"
)

// errNoGUI is --gui on a build without the window: one built without the gui tag, which needs cgo
// and, on Linux, the window system's development libraries.
var errNoGUI = errors.New("this autodoc was built without the GUI: `make build-gui` builds it (on macOS `make build` does), " +
	"with cgo and, on Linux, the window system's development libraries (docs/reference/02-quickstart-and-workspaces.md, §2.1)")

func runGUI(_ context.Context, _ tui.LaunchOptions, owned []owner) error {
	return finishRun(errNoGUI, owned...)
}
