package app

import (
	"os"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui"
)

// prefersVulkan reports whether the window asks for Vulkan first, as tui.gui.renderer's mirror at
// path says (tui.GUIRendererFile): unless it names OpenGL. No mirror (the first start, or one that
// cannot be read) and one the TUI did not write are auto. It is the mirror's one reader.
func prefersVulkan(path string) bool {
	b, err := os.ReadFile(path)
	return err != nil || strings.TrimSpace(string(b)) != tui.RendererOpenGL
}
