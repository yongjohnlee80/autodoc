package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
)

// runHTML is the TUI on a managed daemon that derives HTML, as every build's does (the test
// daemon derives nothing unless told), serving kb; it answers the TUI and kb's root.
func runHTML(t *testing.T) (*running, string) {
	t.Helper()
	reg, err := registrations.New(nil, registrations.Documents(derived.MaxContainer, derived.MaxText))
	if err != nil {
		t.Fatal(err)
	}
	kb := fileDir(t, "a.md", "# A\n")
	d := startManagedWith(t, map[string]string{"kb": kb}, serving.Options{Registrations: reg})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	return r, kb
}

// htmldoc_test.go: an HTML file opened Raw or Simplified (htmldoc.go, files.go's load).

// openHTMLAnswer opens abs and answers the question how.
func (r *running) openHTMLAnswer(t *testing.T, abs, how string) {
	t.Helper()
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.s.WaitForText(t, "open an HTML file")
	r.h.p.Post(func() { r.h.openHTMLAs(how) })
}

// TestOpeningAnHTMLFileAsksSimplifiedOrRaw: Simplified opens an HTML file as it always opened, its
// derived text read-only; Raw opens its own bytes, editable, and Ctrl+S saves them; reading it
// again keeps it raw; Cancel leaves the page as it was.
func TestOpeningAnHTMLFileAsksSimplifiedOrRaw(t *testing.T) {
	r, _ := runHTML(t)
	dir := t.TempDir()
	page, other := filepath.Join(dir, "page.html"), filepath.Join(dir, "other.html")
	for p, body := range map[string]string{page: "<h1>Title</h1><p>body</p>\n", other: "<p>other</p>\n"} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	r.openHTMLAnswer(t, page, "simplified")
	r.waitOpen(t, "", page)
	f := r.file()
	if f.raw || f.derived != "HTML" || strings.Contains(r.editorText(), "<h1>") || !onLoop(r, r.h.core.ReadOnly) {
		t.Fatalf("simplified: raw %v, derived %q, read-only %v, text %q", f.raw, f.derived, onLoop(r, r.h.core.ReadOnly), r.editorText())
	}

	r.h.p.Post(r.h.closeFile)
	r.s.WaitFor(t, "closed", func(string) bool { return !r.file().open })
	r.openHTMLAnswer(t, page, "raw")
	r.waitOpen(t, "", page)
	f = r.file()
	if !f.raw || f.derived != "" || r.editorText() != "<h1>Title</h1><p>body</p>\n" || onLoop(r, r.h.core.ReadOnly) {
		t.Fatalf("raw: raw %v, derived %q, read-only %v, text %q", f.raw, f.derived, onLoop(r, r.h.core.ReadOnly), r.editorText())
	}
	r.typeInEditor(t, "<!-- edited -->")
	r.keys(t, decltest.Ctrl('s'))
	waitDisk(t, r, page, "<!-- edited --><h1>Title</h1><p>body</p>\n")

	r.h.p.Post(func() { r.h.load(r.h.file.ws, r.h.file.path, nil) }) // read again: no question
	r.s.WaitForText(t, "opened "+page)
	if f := r.file(); !f.raw || onLoop(r, func() bool { return r.h.htmlAsk != nil }) {
		t.Errorf("read again: raw %v, asked %v", f.raw, onLoop(r, func() bool { return r.h.htmlAsk != nil }))
	}

	r.openHTMLAnswer(t, other, "cancel")
	r.s.WaitFor(t, "the question closed", func(sc string) bool { return !strings.Contains(sc, "open an HTML file") })
	if f := r.file(); f.path != page || !f.raw {
		t.Errorf("after Cancel the page is %q raw %v, want page.html as it was", f.path, f.raw)
	}
}

// TestARawHTMLFilesPageIsItsRenderedView: under a GUI, a file opened Raw turns the editor's page on
// before its text arrives, and any other file, opened Simplified or not HTML, turns it off; in a
// terminal it is never on.
func TestARawHTMLFilesPageIsItsRenderedView(t *testing.T) {
	r, kb := runHTML(t)
	dir := t.TempDir()
	page := filepath.Join(dir, "page.html")
	if err := os.WriteFile(page, []byte("<p>page</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var seen []bool
	var textAt []string // the editor's text when the page was switched: never the new file's
	r.h.p.Post(func() {
		r.h.nativeViews = func() bool { return true }
		r.h.docSwitch = func(on bool) { seen = append(seen, on); textAt = append(textAt, r.h.core.Value()) }
	})
	r.openHTMLAnswer(t, page, "raw")
	r.waitOpen(t, "", page)
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
	r.h.p.Post(r.h.closeFile)
	r.s.WaitFor(t, "closed", func(string) bool { return !r.file().open })
	r.openHTMLAnswer(t, page, "simplified")
	r.waitOpen(t, "", page)
	got := onLoop(r, func() []bool { return append([]bool(nil), seen...) })
	if len(got) < 4 || !got[0] || got[1] || got[2] || got[len(got)-1] {
		t.Fatalf("the page was switched %v, want on for the raw file, then off for a.md, the draft and a simplified open", got)
	}
	if at := onLoop(r, func() string { return textAt[0] }); strings.Contains(at, "<p>page</p>") {
		t.Errorf("the page was turned on after the file's text arrived: %q", at)
	}

	r.h.p.Post(func() { r.h.nativeViews = func() bool { return false }; seen = nil })
	r.h.p.Post(r.h.closeFile)
	r.openHTMLAnswer(t, page, "raw")
	r.waitOpen(t, "", page)
	for _, on := range onLoop(r, func() []bool { return append([]bool(nil), seen...) }) {
		if on {
			t.Error("a terminal turned the page on")
		}
	}
}

// TestCancellingAnHTMLFileInAnotherWorkspaceStaysPut: the question comes before its workspace is
// entered, so Cancel leaves the workspace and the open file as they were (Lector, #76 r0).
func TestCancellingAnHTMLFileInAnotherWorkspaceStaysPut(t *testing.T) {
	r, kb, _, _ := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
	r.h.p.Post(func() { r.h.openRef(fileRef{"notes", "page.html"}, true, nil) })
	r.s.WaitForText(t, "open an HTML file")
	if ws := onLoop(r, func() string { return r.h.ws }); ws != "kb" {
		t.Fatalf("the question was asked in %q, after entering the other workspace", ws)
	}
	r.h.p.Post(func() { r.h.openHTMLAs("cancel") })
	r.s.WaitFor(t, "the question closed", func(sc string) bool { return !strings.Contains(sc, "open an HTML file") })
	if ws, f := onLoop(r, func() string { return r.h.ws }), r.file(); ws != "kb" || !f.open || f.ws != "kb" || f.path != "a.md" {
		t.Errorf("after Cancel: workspace %q, file %s:%s open %v; want kb with a.md as it was", ws, f.ws, f.path, f.open)
	}
}

// TestAPagesResourcesComeFromItsWorkspace: a workspace file's stylesheets and images are read from
// the workspace's folder, against the file's own; an outside file's from its own folder alone; a
// refused one is said once a page, with what to do.
func TestAPagesResourcesComeFromItsWorkspace(t *testing.T) {
	r, kb := runHTML(t)
	at := func(ws, p string) [2]string {
		return onLoop(r, func() [2]string { a, b := r.h.resourcesFor(ws, p); return [2]string{a, b} })
	}
	if got := at("kb", "Aesop/page.html"); got != [2]string{kb, "Aesop"} {
		t.Errorf("a workspace file's: %q, want %q and Aesop", got, kb)
	}
	if got := at("kb", "top.html"); got != [2]string{kb, ""} {
		t.Errorf("a file at the workspace's root: %q", got)
	}
	if got := at("", "/elsewhere/site/page.html"); got != [2]string{"/elsewhere/site", ""} {
		t.Errorf("an outside file's: %q, want its own folder and no base", got)
	}
	said := func(part string) int {
		return onLoop(r, func() int {
			c := 0
			for _, m := range r.h.notices {
				if strings.Contains(m.text, part) {
					c++
				}
			}
			return c
		})
	}
	// an outside file's page: twice refused, said once; another document in the same folder, said
	// again
	r.h.p.Post(func() {
		r.h.file.ws = ""
		r.h.documentFor("", "/site/a.html", false)
		r.h.resourceRefused("../assets/archive.css")
		r.h.resourceRefused("../assets/x.svg")
	})
	r.s.WaitForText(t, "add the folder as a workspace")
	if n := said("add the folder as a workspace"); n != 1 {
		t.Errorf("one page: said %d times, want once", n)
	}
	r.h.p.Post(func() {
		r.h.documentFor("", "/site/b.html", false)
		r.h.resourceRefused("../assets/archive.css")
	})
	r.s.WaitFor(t, "said for the second page", func(string) bool { return said("add the folder as a workspace") == 2 })
}

// TestRefusalNoticesSayWhy: a refused resource's notice says why, and gives the workspace advice
// only where adding a folder as a workspace would let it load.
func TestRefusalNoticesSayWhy(t *testing.T) {
	for _, c := range []struct{ src, ws, want, not string }{
		{"https://example.com/a.css", "", "web and file: addresses", "workspace"},
		{"file:///etc/a.css", "kb", "web and file: addresses", "workspace"},
		{"//cdn.example/a.js", "", "web and file: addresses", "workspace"},
		{"/abs/a.png", "", "absolute paths", "workspace"},
		{"../../outside.png", "kb", "outside the workspace kb", "add the folder"},
		{"../assets/a.css", "", "add the folder as a workspace", ""},
	} {
		got := refusalNotice(c.src, c.ws)
		if !strings.Contains(got, c.want) || c.not != "" && strings.Contains(got, c.not) || !strings.Contains(got, c.src) {
			t.Errorf("%q in %q: %q, want it to say %q (and not %q)", c.src, c.ws, got, c.want, c.not)
		}
	}
}
