package tui

import (
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// TestARelativePathWithNoWorkspaceIsRefusedWithWhy: an open of a relative path with no workspace
// (none in use yet, say) is refused at once, the status line saying why, and nothing is sent to the
// daemon: before, it went out as a file outside every workspace and was refused there as a bad
// path, the open seeming under way.
func TestARelativePathWithNoWorkspaceIsRefusedWithWhy(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	var mu sync.Mutex
	var sent []string
	sess := NewSession(d.sock, nil)
	sess.SetBeforeCall(func(method string, _ []any) {
		if strings.HasPrefix(method, "file.") || strings.HasPrefix(method, "doc.read") {
			mu.Lock()
			sent = append(sent, method)
			mu.Unlock()
		}
	})
	r := runTUI(t, sess, Options{})
	r.ready(t)
	mu.Lock()
	sent = nil
	mu.Unlock()
	r.h.p.Post(func() { r.h.openRef(feature.FileRef{WS: "", Path: "a.md"}, true, nil) })
	r.waitStatus(t, "open a.md: no workspace is in use, so a relative path names no file; give its full path")
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 0 {
		t.Fatalf("the refused open sent %v to the daemon", sent)
	}
	if f := r.file(); f.open && f.path == "a.md" {
		t.Fatal("a.md opened from a relative path with no workspace")
	}
}
