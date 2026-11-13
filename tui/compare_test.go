package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/diff/merge"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
	"github.com/yongjohnlee80/golib/tui/widget/mergeview"
)

// compare_test.go: the conflict's Compare (compare.go), on a daemon over real folders, the disk
// changed by another program while the buffer has unsaved edits.

// compareView is the Compare dialog's MergeView, read on the loop.
func (r *running) compareView(t *testing.T) *mergeview.MergeView {
	t.Helper()
	v := onLoop(r, func() *mergeview.MergeView {
		c, ok := r.h.p.Find("compareView")
		if !ok {
			return nil
		}
		v, _ := tuidecl.MergeViewOf(c)
		return v
	})
	if v == nil {
		t.Fatal("no Compare view in the terminal's layout")
	}
	return v
}

// conflicted opens a.md ("one\ntwo\nthree\n"), edits its third line to "THREE mine" in the buffer,
// writes disk text over it, and shows it again, asked keep / reload / overwrite / compare.
func conflicted(t *testing.T, disk string) (*running, string) {
	t.Helper()
	r, root := onWideDisk(t)
	r.openOnDisk(t, "a.md")
	onLoop(r, func() bool {
		_, _ = r.h.core.Replace(editor.TextRange{From: editor.TextPos{Line: 2}, To: editor.TextPos{Line: 2, Col: 5}}, "THREE mine")
		return true
	})
	r.s.WaitFor(t, "the edit, unsaved", func(string) bool { return r.file().dirty })
	rewrite(t, root, "a.md", disk)
	r.s.WaitFor(t, "a.md badged", func(string) bool { return diskOf(r, "a.md") == diskChanged })
	r.openOnDisk(t, "b.md")
	r.openByPicker(t, "a.md")
	r.s.WaitForText(t, "while it had unsaved changes")
	return r, root
}

// onWideDisk is onDisk on a screen wide enough for the merge view's bar to show its notes beside
// its buttons.
func onWideDisk(t *testing.T) (*running, string) {
	t.Helper()
	root := fileDir(t, "a.md", "one\ntwo\nthree\n", "b.md", "bbb\n")
	d := startManaged(t, map[string]string{"alpha": root})
	r := runTUISized(t, NewSession(d.sock, nil), Options{Workspace: "alpha"}, 200, 50)
	r.waitAttached(t, "alpha")
	r.prefsIn(t)
	return r, root
}

// compareOpen answers the conflict with Compare and waits for the merge.
func (r *running) compareOpen(t *testing.T) *mergeview.MergeView {
	t.Helper()
	r.keys(t, key('c'))
	r.s.WaitForText(t, "Compare a.md with the disk")
	v := r.compareView(t)
	r.s.WaitFor(t, "the merge loaded", func(string) bool { return onLoop(r, func() bool { return v.Model() != nil }) })
	return v
}

// TestCompareKeepsTheBuffersLinesInAConflictLeft: the disk's change elsewhere merged in, the
// conflict left unresolved, Apply (asked, and answered yes) keeps the buffer's own lines there: the
// result in the buffer, unsaved, at the disk's version, the disk's text its base.
func TestCompareKeepsTheBuffersLinesInAConflictLeft(t *testing.T) {
	t.Parallel()
	r, root := conflicted(t, "ONE\ntwo\nthree disk\n")
	v := r.compareOpen(t)
	if in := onLoop(r, func() merge.Input { return v.Model().Input() }); !in.HasBase || in.Base != "one\ntwo\nthree\n" || in.ResultSide != merge.Right {
		t.Fatalf("the merge's input: base %q (has %v), side %v; want the opened text, the buffer's side", in.Base, in.HasBase, in.ResultSide)
	}
	onLoop(r, func() bool { v.Merge(); v.Apply(); return true })
	r.s.WaitForText(t, "Apply anyway?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "the merge in the buffer", func(string) bool { return r.editorText() == "ONE\ntwo\nTHREE mine\n" })
	disk, _ := os.ReadFile(filepath.Join(root, "a.md"))
	if f := r.file(); !f.dirty || f.disk != diskSame || !f.baseOK || f.base != string(disk) {
		t.Fatalf("after Apply: dirty %v, disk %v, base %q (ok %v); want unsaved, at the disk's version, its text the base", f.dirty, f.disk, f.base, f.baseOK)
	}
	r.undo()
	r.wantText(t, "one undo", "one\ntwo\nTHREE mine\n")
}

// TestASecondCompareMergesAgainstTheTextApplied: after an Apply, the disk moving again is merged
// against the disk's text the Apply took in, never the text first opened.
func TestASecondCompareMergesAgainstTheTextApplied(t *testing.T) {
	t.Parallel()
	r, root := conflicted(t, "ONE\ntwo\nthree\n")
	v := r.compareOpen(t)
	onLoop(r, func() bool { v.Merge(); v.Apply(); return true })
	r.s.WaitFor(t, "the merge in the buffer", func(string) bool { return r.editorText() == "ONE\ntwo\nTHREE mine\n" })
	rewrite(t, root, "a.md", "ONE\ntwo\nthree\nfour\n")
	r.s.WaitFor(t, "a.md badged again", func(string) bool { return diskOf(r, "a.md") == diskChanged })
	r.openOnDisk(t, "b.md")
	r.openByPicker(t, "a.md")
	r.s.WaitForText(t, "while it had unsaved changes")
	v = r.compareOpen(t)
	if in := onLoop(r, func() merge.Input { return v.Model().Input() }); in.Base != "ONE\ntwo\nthree\n" {
		t.Fatalf("the second merge's base %q, want the text the first Apply took in", in.Base)
	}
}

// TestApplyIsRefusedWhenTheBufferMovesUnderTheCompare: an edit to the buffer while the merge is
// open (a script's, an agent's) is offered as a reload; an Apply from before it is refused in the
// bar, and the edit stays.
func TestApplyIsRefusedWhenTheBufferMovesUnderTheCompare(t *testing.T) {
	t.Parallel()
	r, _ := conflicted(t, "ONE\ntwo\nthree\n")
	v := r.compareOpen(t)
	onLoop(r, func() bool {
		_, _ = r.h.core.Replace(editor.TextRange{From: editor.TextPos{Line: 1}, To: editor.TextPos{Line: 1, Col: 3}}, "TWO agent")
		return true
	})
	r.s.WaitForText(t, "A side changed under the merge")
	onLoop(r, func() bool { v.AcceptLeft(); v.Apply(); return true })
	r.s.WaitForText(t, "The buffer changed since the merge was loaded")
	if got := r.editorText(); got != "one\nTWO agent\nTHREE mine\n" {
		t.Fatalf("the buffer after the refused Apply: %q, want the agent's edit kept", got)
	}
}

// TestAClosedCompareNeverHoldsTheDiskCheck: the disk moving again and again, the Compare open and
// then closed, each check still lands (its notice to the view is one slot, never waited on).
func TestAClosedCompareNeverHoldsTheDiskCheck(t *testing.T) {
	t.Parallel()
	r, root := conflicted(t, "ONE\ntwo\nthree\n")
	v := r.compareOpen(t)
	version := func() string { return onLoop(r, func() string { return r.h.active.file.diskVersion }) }
	for i, text := range []string{"x1\n", "x2\n", "x3\n"} {
		was := version()
		rewrite(t, root, "a.md", text)
		r.s.WaitFor(t, "the check of rewrite "+string(rune('1'+i)), func(string) bool { return version() != was })
	}
	onLoop(r, func() bool { v.Cancel(); return true })
	r.s.WaitForText(t, "while it had unsaved changes")
	for i, text := range []string{"y1\n", "y2\n"} {
		was := version()
		rewrite(t, root, "a.md", text)
		r.s.WaitFor(t, "the check after closing "+string(rune('1'+i)), func(string) bool { return version() != was })
	}
}

// TestAFileGoneUnderTheCompareIsSaidSo: a file removed while its merge is open is said gone from
// disk on a reload, never "not text", and the merge shown stays.
func TestAFileGoneUnderTheCompareIsSaidSo(t *testing.T) {
	t.Parallel()
	r, root := conflicted(t, "ONE\ntwo\nthree\n")
	v := r.compareOpen(t)
	if err := os.Remove(filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { v.Reload(); return true })
	r.s.WaitForText(t, "a.md is gone from disk")
	if strings.Contains(r.s.String(), "Not text") {
		t.Fatalf("a gone file said as not text:\n%s", r.s.String())
	}
}

// TestCancelReturnsToTheQuestion: the Compare's Cancel puts the conflict's question back.
func TestCancelReturnsToTheQuestion(t *testing.T) {
	t.Parallel()
	r, _ := conflicted(t, "ONE\ntwo\nthree\n")
	v := r.compareOpen(t)
	onLoop(r, func() bool { v.Cancel(); return true })
	r.s.WaitForText(t, "while it had unsaved changes")
	if got := r.editorText(); got != "one\ntwo\nTHREE mine\n" {
		t.Fatalf("Cancel changed the buffer: %q", got)
	}
}

// TestCompareWithTheDisksOwnTextOffersItsVersion: a disk holding the buffer's own text has nothing
// to merge: Compare asks to take the disk's version, the buffer then saved.
func TestCompareWithTheDisksOwnTextOffersItsVersion(t *testing.T) {
	t.Parallel()
	r, _ := conflicted(t, "one\ntwo\nTHREE mine\n")
	r.keys(t, key('c'))
	r.s.WaitForText(t, "Nothing to merge")
	r.keys(t, key('t'))
	r.s.WaitFor(t, "the buffer saved", func(string) bool { f := r.file(); return !f.dirty && f.disk == diskSame })
	if f := r.file(); f.base != "one\ntwo\nTHREE mine\n" {
		t.Fatalf("the base after taking the disk's version: %q", f.base)
	}
}

// TestTheSaveConflictOffersCompare: a save over a newer version on disk asks the same question,
// Compare among its answers.
func TestTheSaveConflictOffersCompare(t *testing.T) {
	t.Parallel()
	r, root := onDisk(t, Options{})
	r.openOnDisk(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	rewrite(t, root, "a.md", "other\n")
	r.s.WaitFor(t, "a.md badged", func(string) bool { return diskOf(r, "a.md") == diskChanged })
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "changed on disk since you opened it")
	r.s.WaitForText(t, "Compare")
}

// TestOnlyATerminalsLayoutHasTheCompare: the Compare dialog is written into main.qml for a terminal
// alone; the window's layout and the autodoc.dialogs components, which every build parses, never
// name the MergeView a window's toolkit lacks.
func TestOnlyATerminalsLayoutHasTheCompare(t *testing.T) {
	t.Parallel()
	src := layout
	if got := string(withCompare(src, true)); strings.Contains(got, "MergeView") {
		t.Fatal("the window's layout names MergeView")
	}
	if got := string(withCompare(src, false)); !strings.Contains(got, "MergeView") || !strings.Contains(got, "id: compareView") {
		t.Fatal("the terminal's layout has no Compare")
	}
	err := fs.WalkDir(qmlFiles, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".qml") {
			return err
		}
		b, _ := fs.ReadFile(qmlFiles, p)
		if strings.Contains(string(b), "MergeView") {
			t.Errorf("%s names MergeView: every build parses it", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestABaseOverTheBoundIsNotKept: a text past compareBaseMax keeps no base (Compare merges two-way);
// one within keeps it, set with its version.
func TestABaseOverTheBoundIsNotKept(t *testing.T) {
	t.Parallel()
	var f openedFile
	f.setAgreed("v1", "small")
	if !f.baseOK || f.base != "small" || f.version != "v1" {
		t.Fatalf("within the bound: %+v", f)
	}
	f.setAgreed("v2", strings.Repeat("x", compareBaseMax+1))
	if f.baseOK || f.base != "" || f.version != "v2" {
		t.Fatalf("past the bound: base kept %v, version %q", f.baseOK, f.version)
	}
}

// TestACompareNoticeNeverWaits: the notice of a change under the view is one slot, sent without
// waiting: with no view reading it (one being torn down, say), the disk's check and an edit that
// tell it go on.
func TestACompareNoticeNeverWaits(t *testing.T) {
	t.Parallel()
	s := newCompareSource(nil, nil)
	done := make(chan struct{})
	go func() {
		for range 3 {
			s.saw()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a notice waited for a reader: the disk's check would stall")
	}
	if len(s.Changed()) != 1 {
		t.Fatalf("%d notices held, want the one slot full", len(s.Changed()))
	}
}
