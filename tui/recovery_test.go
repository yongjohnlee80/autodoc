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

// A kept copy whose file is gone is restored as the untitled draft, and the next copy is asked
// about.
func TestACopyWhoseFileIsGoneBecomesTheDraft(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_ = writeRecovery(dir, recovered{WS: "kb", Path: "gone.md", Text: "the lost text\n", Saved: now})
	_ = writeRecovery(dir, recovered{WS: "kb", Path: "a.md", Text: "older\n", Saved: now.Add(-time.Hour)})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitForText(t, "gone.md had unsaved changes")
	r.keys(t, key('r'))
	r.s.WaitForText(t, "a.md had unsaved changes") // the next one is asked
	if got := r.editorText(); got != "the lost text\n" {
		t.Fatalf("the draft holds %q", got)
	}
	if f := r.file(); f.open || !f.dirty {
		t.Fatalf("the draft: open %v, dirty %v", f.open, f.dirty)
	}
	// the restored text is unsaved: its copy stays until it is saved or discarded
	if got := readRecovery(dir); len(got) != 2 {
		t.Fatalf("copies left: %+v; want both", got)
	}
	r.keys(t, key('l')) // a.md: Later
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "had unsaved changes") })
	// an unasked exit now keeps the draft's text as the draft's copy, replacing gone.md's
	if err := onLoop(r, r.h.keepRecovery); err != nil {
		t.Fatal(err)
	}
	got := readRecovery(dir)
	if len(got) != 2 || got[0].Path != "" || got[0].Text != "the lost text\n" || got[1].Path != "a.md" {
		t.Fatalf("after an unasked exit: %+v; want the draft's copy and a.md's", got)
	}
}

// A restored copy stays until its text is discarded; then it goes.
func TestARestoredCopyStaysUntilDiscarded(t *testing.T) {
	dir := t.TempDir()
	_ = writeRecovery(dir, recovered{WS: "kb", Path: "gone.md", Text: "lost\n", Saved: time.Now()})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitForText(t, "gone.md had unsaved changes")
	r.keys(t, key('r'))
	r.s.WaitFor(t, "the draft restored", func(string) bool { return r.editorText() == "lost\n" })
	if len(readRecovery(dir)) != 1 {
		t.Fatal("the copy went with the text still unsaved")
	}
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	r.openByPicker(t, "a.md")
	r.s.WaitForText(t, "Save them before you open a.md?")
	r.keys(t, key('d'))
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the copy discarded", func(string) bool { return len(readRecovery(dir)) == 0 })
}

// Restoring the copy of the document open now puts its text in at once.
func TestACopyOfTheOpenDocumentGoesInAtOnce(t *testing.T) {
	dir := t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool {
		r.h.restoreRecovered(recovered{WS: "kb", Path: "a.md", Text: "kept\n", Saved: time.Now()})
		return true
	})
	r.s.WaitForText(t, "kept")
	if waiting := onLoop(r, func() bool { return r.h.restoreWaiting || r.h.pendingRestore != nil }); waiting {
		t.Fatal("a restore of the open document is left waiting")
	}
}

// Stay in the unsaved question before a restore keeps that copy for the next start, and nothing
// waits for an open that will not come.
func TestStayBeforeARestoreKeepsTheCopy(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_ = writeRecovery(dir, recovered{WS: "kb", Path: "b.md", Text: "b kept\n", Saved: now})
	_ = writeRecovery(dir, recovered{WS: "kb", Path: "a.md", Text: "a kept\n", Saved: now.Add(-time.Hour)})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a disk\n", "b.md", "b disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitForText(t, "b.md had unsaved changes")
	r.keys(t, key('r'))
	r.s.WaitForText(t, "a.md had unsaved changes")
	if got := r.editorText(); got != "b kept\n" {
		t.Fatalf("b.md holds %q", got)
	}
	r.keys(t, key('r'))
	r.s.WaitForText(t, "Save them before you open a.md?") // b.md's restored text is unsaved
	r.keys(t, key('t'))
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "before you open") })
	if waiting := onLoop(r, func() bool { return r.h.restoreWaiting || r.h.pendingRestore != nil }); waiting {
		t.Fatal("Stay left the restore waiting")
	}
	// a.md's copy kept for the next start; b.md's stays while its restored text is unsaved
	if got := readRecovery(dir); len(got) != 2 {
		t.Fatalf("copies left: %+v; want both", got)
	}
}

// :q! deletes a copy kept earlier for the document it discards.
func TestQBangDeletesAnOlderCopy(t *testing.T) {
	dir := t.TempDir()
	_ = writeRecovery(dir, recovered{WS: "kb", Path: "a.md", Text: "older\n", Saved: time.Now()})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitForText(t, "had unsaved changes")
	r.keys(t, key('l')) // Later
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "had unsaved changes") })
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.command(t, "q!")
	select {
	case <-r.s.Quit():
	case <-time.After(3 * time.Second):
		t.Fatal(":q! did not quit")
	}
	if got := readRecovery(dir); len(got) != 0 {
		t.Fatalf(":q! left %+v", got)
	}
}

// A copy whose file was gone, restored as the draft, goes once the draft is saved under a name.
func TestARestoredDraftsCopyGoesWhenTheDraftIsSaved(t *testing.T) {
	dir := t.TempDir()
	_ = writeRecovery(dir, recovered{WS: "kb", Path: "gone.md", Text: "lost\n", Saved: time.Now()})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitForText(t, "gone.md had unsaved changes")
	r.keys(t, key('r'))
	r.s.WaitFor(t, "the draft restored", func(string) bool { return r.editorText() == "lost\n" })
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "save the draft")
	r.keys(t, decltest.Type("saved")...)
	r.keys(t, enter())
	r.waitFile(t, "saved.md")
	if got := d.read(t, "kb", "saved.md"); got != "lost\n" {
		t.Fatalf("saved.md holds %q", got)
	}
	r.s.WaitFor(t, "the copy gone", func(string) bool { return len(readRecovery(dir)) == 0 })
}

// Saving a draft drops only the copy it was restored from: an older draft's copy, kept with Later,
// stays for the next start.
func TestSavingADraftKeepsAnotherDraftsCopy(t *testing.T) {
	dir := t.TempDir()
	// a draft's copy, as keepRecovery writes it: no file open, so no workspace and no path
	_ = writeRecovery(dir, recovered{WS: "", Path: "", Text: "an old draft\n", Saved: time.Now()})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "on disk\n"}})
	r := attachedWith(t, d, dir)
	r.s.WaitForText(t, "untitled had unsaved changes")
	r.keys(t, key('l')) // Later
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "had unsaved changes") })
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	r.typeInEditor(t, "a new draft")
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "save the draft")
	r.keys(t, decltest.Type("new")...)
	r.keys(t, enter())
	r.waitFile(t, "new.md")
	if got := readRecovery(dir); len(got) != 1 || got[0].Text != "an old draft\n" {
		t.Fatalf("copies after saving another draft: %+v; want the old draft's kept", got)
	}
}
