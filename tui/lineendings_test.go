package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file's line breaks are read as CRLF when most of them are, and the editor's LF text is written
// back with them; LF, and a file with no line break, stay LF.
func TestAFilesLineBreaksAreReadAndWrittenBack(t *testing.T) {
	for _, c := range []struct {
		text string
		crlf bool
	}{
		{"a\r\nb\r\n", true},
		{"a\r\nb\r\nc\n", true}, // mostly CRLF
		{"a\nb\nc\r\n", false},  // mostly LF
		{"a\nb\n", false},
		{"no break", false},
		{"", false},
	} {
		if got := usesCRLF(c.text); got != c.crlf {
			t.Errorf("usesCRLF(%q) = %v, want %v", c.text, got, c.crlf)
		}
	}
	if got := withEndings("a\nb\n", true); got != "a\r\nb\r\n" {
		t.Errorf("withEndings CRLF = %q", got)
	}
	if got := withEndings("a\nb\n", false); got != "a\nb\n" {
		t.Errorf("withEndings LF = %q", got)
	}
	if got := asEditorText("a\r\nb\rc\n"); got != "a\nb\nc\n" {
		t.Errorf("asEditorText = %q", got)
	}
}

// openAndSave opens name in kb's workspace, puts text in the editor and saves it, then answers the
// file's bytes on disk.
func openAndSave(t *testing.T, r *running, kb, name, text string) string {
	t.Helper()
	r.h.p.Post(func() { r.h.openPath(name) })
	r.waitFile(t, name)
	r.h.p.Post(func() {
		r.h.editor.SetValue(text)
		r.h.edited()
		r.h.save()
	})
	var got string
	r.s.WaitFor(t, name+" saved", func(string) bool {
		b, _ := os.ReadFile(filepath.Join(kb, name))
		got = string(b)
		return !onLoop(r, func() bool { return r.h.file.dirty }) && strings.Contains(got, "edited")
	})
	return got
}

// A file that uses CRLF is saved with CRLF on every line, the one edited included, so a Windows
// user's file changes only where it was edited; a file that uses LF, and a new one, are saved with
// LF.
func TestASaveKeepsTheFilesLineBreaks(t *testing.T) {
	kb := fileDir(t, "windows.md", "# Notes\r\n\r\nfirst line\r\nsecond line\r\n", "unix.md", "# Notes\n\nfirst line\n")
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")

	got := openAndSave(t, r, kb, "windows.md", "# Notes\n\nfirst line edited\nsecond line\n")
	if want := "# Notes\r\n\r\nfirst line edited\r\nsecond line\r\n"; got != want {
		t.Errorf("the CRLF file was saved as %q, want %q", got, want)
	}
	got = openAndSave(t, r, kb, "unix.md", "# Notes\n\nfirst line edited\n")
	if strings.Contains(got, "\r") {
		t.Errorf("the LF file was saved with a CR: %q", got)
	}
}

// A CRLF file changed on disk while open is reloaded as the editor holds text, LF: no line keeps a
// CR, and a save after writes it back with CRLF.
func TestACRLFFileReloadedFromDiskKeepsNoCR(t *testing.T) {
	kb := fileDir(t, "windows.md", "# Notes\r\n\r\nfirst line\r\n")
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	r.h.p.Post(func() { r.h.openPath("windows.md") })
	r.waitFile(t, "windows.md")
	if err := os.WriteFile(filepath.Join(kb, "windows.md"), []byte("# Notes\r\n\r\nchanged on disk\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the change on disk reloaded", func(string) bool {
		return onLoop(r, func() bool { return strings.Contains(r.h.core.Value(), "changed on disk") })
	})
	// exactly the file's lines: no CR kept, and none turned into an extra line break
	if v := onLoop(r, func() string { return r.h.core.Value() }); v != "# Notes\n\nchanged on disk\n" {
		t.Fatalf("the reloaded text is %q, want %q", v, "# Notes\n\nchanged on disk\n")
	}
	got := openAndSave(t, r, kb, "windows.md", "# Notes\n\nchanged on disk, then edited\n")
	if want := "# Notes\r\n\r\nchanged on disk, then edited\r\n"; got != want {
		t.Errorf("saved after the reload as %q, want %q", got, want)
	}
}
