package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// htmlpane_test.go: File › Preview HTML under --gui (htmlpane.go) — the note's page in the pane
// beside the editor, live and following the cursor, its links opening notes, files and the
// browser; in a terminal Preview HTML is what it was.

// longNote is a note of n paragraphs after a heading, linking to b.
func longNote(n int) string {
	var sb strings.Builder
	sb.WriteString("# Heading\n\nsee [[b]] and [the web](https://example.com)\n\n")
	for i := range n {
		sb.WriteString("paragraph " + string(rune('a'+i%26)) + " of the note\n\n")
	}
	return sb.String()
}

// runPane is the TUI on a managed daemon whose kb holds a.md (a long note) and b.md, standing in
// for a GUI window, with a.md open; it answers the TUI and the kb's root.
func runPane(t *testing.T) (*running, string) {
	t.Helper()
	r, kb, _, _ := runOutside(t)
	if err := os.WriteFile(filepath.Join(kb, "a.md"), []byte(longNote(40)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kb, "b.md"), []byte("# B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() { r.h.nativeViews = func() bool { return true } })
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
	return r, kb
}

func (r *running) paneSource() string {
	return onLoop(r, func() string {
		v, ok := r.h.htmlView()
		if !ok {
			return ""
		}
		return string(v.Source())
	})
}

// TestPreviewHTMLUnderAGUIOpensThePane: Preview HTML opens the pane on the note's page, its blocks
// carrying their source bytes; an edit reaches it after the quiet; the cursor's block comes to its
// top.
func TestPreviewHTMLUnderAGUIOpensThePane(t *testing.T) {
	r, _ := runPane(t)
	r.h.p.Post(r.h.previewHTML)
	r.s.WaitFor(t, "the pane open on the page", func(string) bool {
		return onLoop(r, r.h.htmlPaneShown) && strings.Contains(r.paneSource(), `<h1 data-src="0-9">Heading</h1>`)
	})
	r.s.WaitForText(t, "┌ preview")

	r.typeInEditor(t, "fresh ")
	r.s.WaitFor(t, "the edit in the pane", func(string) bool { return strings.Contains(r.paneSource(), "fresh ") })

	var top float32
	r.h.p.Post(func() {
		r.h.core.SetLine(len(r.h.core.Lines())-2, 0)
		r.h.cursorMoved()
	})
	r.s.WaitFor(t, "the pane at the cursor's block", func(string) bool {
		top = onLoop(r, func() float32 { v, _ := r.h.htmlView(); return v.ScrollY() })
		return top > 0
	})
}

// TestPreviewHTMLInATerminalKeepsItsPath: with no GUI, Preview HTML never opens the pane.
func TestPreviewHTMLInATerminalKeepsItsPath(t *testing.T) {
	r, _ := runPane(t)
	opened := make(chan string, 1)
	r.h.p.Post(func() {
		r.h.nativeViews = nil
		r.h.browser = func(_ context.Context, p string) error { opened <- p; return nil }
	})
	if onLoop(r, r.h.native) {
		t.Fatal("the test backend draws native views")
	}
	r.h.p.Post(r.h.previewHTML)
	// today's path: the page written to the cache and handed to the browser (the test terminal
	// draws no images)
	if got := <-opened; !strings.HasSuffix(got, ".html") {
		t.Errorf("the browser was given %q, want the exported page", got)
	}
	if onLoop(r, r.h.htmlPaneShown) {
		t.Error("a terminal's Preview HTML opened the native pane")
	}
}

// TestThePanesLinks: a page's link opens the note it names (a wikilink's page), a file: URL opens
// that file, and a web address goes to the browser.
func TestThePanesLinks(t *testing.T) {
	r, kb := runPane(t)
	browsed := make(chan string, 1)
	r.h.p.Post(func() {
		r.h.browser = func(_ context.Context, p string) error { browsed <- p; return nil }
	})
	r.h.p.Post(func() { r.h.htmlLink("b") })
	r.waitOpen(t, "kb", "b.md")
	r.h.p.Post(func() { r.h.htmlLink("https://example.com") })
	if got := <-browsed; got != "https://example.com" {
		t.Errorf("the browser was given %q", got)
	}
	r.h.p.Post(func() { r.h.htmlLink("file://" + filepath.ToSlash(filepath.Join(kb, "a.md"))) })
	r.waitOpen(t, "kb", "a.md")
}

// TestThePaneFollowsTheOpenNote: with the pane open, another note opened shows its page at once
// (no typing reports it), a render of the old note still on its way never lands over it, the draft
// after a close shows an empty page, and a file that is not Markdown closes the pane.
func TestThePaneFollowsTheOpenNote(t *testing.T) {
	r, kb := runPane(t)
	if err := os.WriteFile(filepath.Join(kb, "c.yaml"), []byte("k: v\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(r.h.previewHTML)
	r.s.WaitFor(t, "a's page", func(string) bool { return strings.Contains(r.paneSource(), ">Heading</h1>") })

	// a's render on its way when b opens
	r.h.p.Post(func() {
		r.h.renderHTMLPane()
		r.h.openAbsolute(filepath.Join(kb, "b.md"))
	})
	r.waitOpen(t, "kb", "b.md")
	r.s.WaitFor(t, "b's page", func(string) bool {
		s := r.paneSource()
		return strings.Contains(s, ">B</h1>") && !strings.Contains(s, "Heading")
	})
	time.Sleep(2 * htmlPaneDelay) // anything of a's still landing would have by now
	if s := r.paneSource(); !strings.Contains(s, ">B</h1>") {
		t.Fatalf("a's page landed over b's: %q", s)
	}

	r.h.p.Post(r.h.closeFile)
	r.s.WaitFor(t, "the draft's empty page", func(string) bool {
		s := r.paneSource()
		return s != "" && !strings.Contains(s, ">B</h1>")
	})
	if !onLoop(r, r.h.htmlPaneShown) {
		t.Fatal("closing the note closed the pane")
	}

	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "c.yaml")) })
	r.waitOpen(t, "kb", "c.yaml")
	r.s.WaitFor(t, "the pane closed on YAML", func(string) bool { return !onLoop(r, r.h.htmlPaneShown) })
}

var _ = widget.DirImages
