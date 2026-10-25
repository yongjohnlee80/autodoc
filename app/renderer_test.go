package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui"
)

// The window asks for Vulkan first unless tui.gui.renderer's mirror names OpenGL: with no mirror
// (the first start), with auto, with what the TUI never writes, and with one that cannot be read.
func TestTheWindowPrefersVulkanUnlessTheMirrorSaysOpenGL(t *testing.T) {
	dir := t.TempDir()
	write := func(name, v string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, c := range []struct {
		name, path string
		vulkan     bool
	}{
		{"no mirror", filepath.Join(dir, "none"), true},
		{"auto", write("auto", tui.RendererAuto+"\n"), true},
		{"opengl", write("opengl", tui.RendererOpenGL+"\n"), false},
		{"garbage", write("garbage", "metal\n"), true},
		{"unreadable", dir, true}, // a directory
	} {
		if got := prefersVulkan(c.path); got != c.vulkan {
			t.Errorf("%s: prefersVulkan %v, want %v", c.name, got, c.vulkan)
		}
	}
}
