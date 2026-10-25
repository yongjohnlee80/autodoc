//go:build gui

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui"
)

// The window's options: one, the zero-copy renderer (Vulkan first), unless the mirror names OpenGL.
func TestTheWindowsRendererOptionsFollowTheMirror(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gui-renderer")
	if n := len(rendererOptions(p)); n != 1 {
		t.Fatalf("no mirror: %d options, want the zero-copy renderer's", n)
	}
	if err := os.WriteFile(p, []byte(tui.RendererOpenGL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := len(rendererOptions(p)); n != 0 {
		t.Fatalf("opengl: %d options, want none", n)
	}
}
