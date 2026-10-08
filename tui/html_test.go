package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The preview, written to the cache, reads the file's relative links from the file's own folder.
func TestHTMLPreviewOpensTheThemedFileInBrowser(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	root := fileDir(t, "n.md", "# On disk\n")
	d := startManaged(t, map[string]string{"kb": root})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	opened := make(chan string, 1)
	r.h.p.Post(func() {
		r.h.browser = func(_ context.Context, path string) error {
			opened <- path
			return nil
		}
		r.h.show("kb", "n.md", "# Unsaved heading\n\n[other](other.md#s)\n", "version", false)
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
		if want := `href="file://` + filepath.ToSlash(root) + `/other.md#s"`; !strings.Contains(string(content), want) {
			t.Fatalf("the preview lacks %s", want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTML preview did not launch browser")
	}
}
