package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// A draft is saved as any text file. A name its workspace indexes (notes.txt) is that workspace's
// file, written by doc.write. Any other name (a .csv, .go or .sql) is written beside it as a file
// outside every workspace (file.write), as File › New file writes one. An HTML file is written as its
// own bytes, and opens as them. Each is the draft's buffer after, holding the text on disk.
func TestADraftIsSavedAsAnyTextFile(t *testing.T) {
	r, kb, _, _ := runOutside(t)
	for _, c := range []struct{ name, text, ws string }{
		{"notes.txt", "plain\n", "kb"},
		{"sample.csv", "a,b\n1,2\n", ""},
		{"main.go", "package main\n", ""},
		{"q.sql", "SELECT 1;\n", ""},
		{"page.html", "<p>hi</p>\n", ""},
	} {
		if err := r.newBufferRun(c.name, c.text); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		r.keys(t, decltest.Ctrl('s'))
		r.s.WaitForText(t, "save the draft")
		r.keys(t, enter()) // the field holds the draft's name
		abs := filepath.Join(kb, c.name)
		p := abs
		if c.ws != "" {
			p = c.name
		}
		r.waitOpen(t, c.ws, p)
		waitDisk(t, r, abs, c.text)
		if got, raw := r.editorText(), onLoop(r, func() bool { return r.h.file.raw }); got != c.text || raw != (c.name == "page.html") {
			t.Fatalf("%s: the buffer holds %q, raw %v; want the text written, raw only for HTML", c.name, got, raw)
		}
	}
}

// File › New file in a workspace makes an empty file the same way, and so does the dialog that
// takes any path: an HTML file at an absolute path too.
func TestNewFileMakesAnyTextFile(t *testing.T) {
	r, kb, _, _ := runOutside(t)
	onLoop(r, func() bool { r.h.createFile("empty.csv"); return true }) // no draft being named: New file
	abs := filepath.Join(kb, "empty.csv")
	r.waitOpen(t, "", abs)
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("empty.csv: %v", err)
	}
	html := filepath.Join(kb, "new.html")
	onLoop(r, func() bool { r.h.createAbsolute(html); return true })
	r.waitOpen(t, "", html)
	if !onLoop(r, func() bool { return r.h.file.raw }) {
		t.Fatal("new.html opened as its derived text, not its own bytes")
	}
}

// A create refused says why, as a notification, not only under the name field: a file that exists,
// left as it was; a draft that is not text (a NUL in it), which writes nothing; and a name that
// leaves the workspace, a draft's or New file's, through .. or a linked folder, which writes nothing
// anywhere.
func TestARefusedCreateIsSaid(t *testing.T) {
	r, kb, _, _ := runOutside(t)
	taken := filepath.Join(kb, "taken.csv")
	if err := os.WriteFile(taken, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.newBufferRun("taken.csv", "new\n"); err != nil {
		t.Fatal(err)
	}
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "save the draft")
	r.keys(t, enter())
	r.waitNotice(t, "create taken.csv: taken.csv exists")
	if b, _ := os.ReadFile(taken); string(b) != "x\n" {
		t.Fatalf("taken.csv was written over: %q", b)
	}
	onLoop(r, func() bool { r.h.closeDialog("fileName"); return true })

	if err := r.newBufferRun("bin.csv", "x"); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool {
		r.h.core.SetValue("a\x00b")
		r.h.nameDraft(nil)
		r.h.createFile("bin.csv")
		return true
	})
	r.waitNotice(t, "create bin.csv: not a text file")
	if _, err := os.Stat(filepath.Join(kb, "bin.csv")); !os.IsNotExist(err) {
		t.Fatalf("bin.csv was written: %v", err)
	}

	// names that leave the workspace: through .., and through a link to a folder outside it
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(kb, "link")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, said string
		draft      bool
	}{
		{"../escape.csv", "../escape.csv", true},
		{"a/../../x.csv", "a/../../x.csv", false}, // File › New file
		{"/sub/../../away.csv", "sub/../../away.csv", false},
		{"link/linked.csv", "link/linked.csv", true},
	} {
		onLoop(r, func() bool {
			r.h.closeDialog("fileName")
			r.h.draft = nil
			if c.draft {
				r.h.core.SetValue("escaped\n")
				r.h.nameDraft(nil)
			}
			r.h.createFile(c.name)
			return true
		})
		r.waitNotice(t, "create "+c.said+": "+c.said+" is not in this workspace")
	}
	for _, out := range []string{filepath.Join(filepath.Dir(kb), "escape.csv"), filepath.Join(filepath.Dir(kb), "x.csv"),
		filepath.Join(filepath.Dir(kb), "away.csv"), filepath.Join(outside, "linked.csv")} {
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("%s was written outside the workspace: %v", out, err)
		}
	}
}
