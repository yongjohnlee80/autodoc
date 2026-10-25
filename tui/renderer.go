package tui

import (
	"os"
	"slices"
	"strconv"
)

// RENDERER — tui.gui.renderer, the window's renderer: auto, Vulkan first where the window can have
// it and OpenGL where it can't (golib's WithZeroCopyFrames), or opengl, OpenGL always. The window
// opens before the store is read, so the row mirrors its value into the state directory
// (Options.RendererFile, GUIRendererFile), and the GUI build reads that as the next start opens its
// window (app/renderer.go). A change applies from the next start. The terminal build ignores it.

// The renderers tui.gui.renderer names.
const (
	RendererAuto   = "auto"
	RendererOpenGL = "opengl"
)

var renderers = []string{RendererAuto, RendererOpenGL}

// mirrorRenderer writes v to the state directory's mirror, as last-workspace is written: for the
// next start, and nothing waits on it. The tui.gui.renderer row's State is its one writer, so it
// follows the store whenever the preference is read or set.
func (h *Host) mirrorRenderer(v string) {
	if h.rendererFile == "" || v == h.rendererMirrored {
		return
	}
	if os.WriteFile(h.rendererFile, []byte(v+"\n"), 0o600) == nil {
		h.rendererMirrored = v // only once written: a write that failed is tried again at the next
	}
}

// setRenderer is Options › Renderer: the preference, which the window takes at the next start.
func (h *Host) setRenderer(name string) {
	if !slices.Contains(renderers, name) {
		h.notify(h.trf("autodoc.notice.renderer.no_renderer_s", strconv.Quote(name)))
		return
	}
	h.setPref(prefRenderer, name, func(p *prefs) { p.renderer = name })
	h.notify(h.tr("autodoc.notice.renderer.from_the_next_start"))
}
