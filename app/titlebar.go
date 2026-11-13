package app

import (
	"os"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui"
)

// ownsTitleBar reports whether the window draws AutoDoc's own title bar, as tui.gui.titlebar's
// mirror at path says (tui.GUITitleBarFile): only when it names AutoDoc's own. No mirror (the first
// start, or one that cannot be read) and one the TUI did not write are the system's. It is the
// mirror's one reader.
func ownsTitleBar(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(b)) == tui.TitleBarOwn
}
