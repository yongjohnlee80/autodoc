package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTMLPreviewOpensTheThemedFileInBrowser(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	d := startManaged(t, map[string]string{"kb": noteDir(t, "n.md", "# On disk\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	opened := make(chan string, 1)
	r.h.p.Post(func() {
		r.h.browser = func(_ context.Context, path string) error {
			opened <- path
			return nil
		}
		r.h.show("n.md", "# Unsaved heading\n", "version")
		r.h.previewHTML()
	})
	select {
	case path := <-opened:
		if !strings.HasPrefix(path, filepath.Join(cache, "autodoc", "previews")) {
			t.Fatalf("preview path = %q", path)
		}
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), "<h1>Unsaved heading</h1>") || !strings.Contains(string(content), "color-scheme:dark") {
			t.Fatalf("preview = %q, %v", content, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTML preview did not launch browser")
	}
}
