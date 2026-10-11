package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// attachedWith is attached with a recovery folder: unsaved work is kept there and offered back.
func attachedWith(t *testing.T, d *daemon, dir string) *running {
	t.Helper()
	r := runTUI(t, NewSession(d.sock, nil), Options{RecoveryDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	return r
}

// The open document, unsaved when the program ends unasked, is kept; a clean one is not.
func TestUnsavedWorkIsKeptWhenTheProgramEndsUnasked(t *testing.T) {
	dir := t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	if err := onLoop(r, r.h.keepRecovery); err != nil {
		t.Fatal(err)
	}
	if got := readRecovery(dir); len(got) != 0 {
		t.Fatalf("a clean document was kept: %+v", got)
	}
	r.typeInEditor(t, "X")
	r.s.WaitFor(t, "the edit unsaved", func(string) bool { return r.file().dirty })
	if err := onLoop(r, r.h.keepRecovery); err != nil {
		t.Fatal(err)
	}
	got := readRecovery(dir)
	if len(got) != 1 || got[0].WS != "kb" || got[0].Path != "a.md" || got[0].Text != "Xaaa\n" {
		t.Fatalf("kept %+v", got)
	}
	if fi, err := os.Stat(recoveryFile(dir, "kb", "a.md")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the copy: %v, %v; want it the user's alone", fi, err)
	}
}

// The next start offers the copy: Restore opens the document with the kept text, unsaved, and
// saving it removes the copy.
func TestTheNextStartOffersAKeptCopyBack(t *testing.T) {
	dir := t.TempDir()
	if err := writeRecovery(dir, recovered{WS: "kb", Path: "a.md", Text: "kept text\n", Saved: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitForText(t, "a.md had unsaved changes when AutoDoc last closed")
	r.s.WaitForText(t, "a copy for you, just in case. Restore it?")
	r.keys(t, key('r'))
	r.s.WaitForText(t, "kept text")
	r.s.WaitFor(t, "the restored text unsaved", func(string) bool { return r.file().dirty })
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saved a.md")
	if got := d.read(t, "kb", "a.md"); got != "kept text\n" {
		t.Fatalf("on disk: %q", got)
	}
	r.s.WaitFor(t, "the copy removed", func(string) bool { return len(readRecovery(dir)) == 0 })
}

// Discard deletes the copy; Later keeps it.
func TestDiscardDeletesTheCopyAndLaterKeepsIt(t *testing.T) {
	dir := t.TempDir()
	_ = writeRecovery(dir, recovered{WS: "kb", Path: "a.md", Text: "one", Saved: time.Now()})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitForText(t, "had unsaved changes")
	r.keys(t, key('l'))
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "had unsaved changes") })
	if len(readRecovery(dir)) != 1 {
		t.Fatal("Later removed the copy")
	}

	r2 := attachedWith(t, startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}}), dir)
	r2.s.WaitForText(t, "had unsaved changes")
	r2.keys(t, key('d'))
	r2.s.WaitFor(t, "the copy deleted", func(string) bool { return len(readRecovery(dir)) == 0 })
}

// Discarding in the unsaved question, and q!, leave nothing to keep.
func TestADiscardedEditIsNotKept(t *testing.T) {
	dir := t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.s.WaitFor(t, "the edit unsaved", func(string) bool { return r.file().dirty })
	r.command(t, "q!")
	select {
	case <-r.s.Quit():
	case <-time.After(3 * time.Second):
		t.Fatal(":q! did not quit")
	}
	if err := r.h.keepRecovery(); err != nil || len(readRecovery(dir)) != 0 {
		t.Fatalf("q! kept a copy: %v %+v", err, readRecovery(dir))
	}
}
