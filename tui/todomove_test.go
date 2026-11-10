package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// todomove_test.go: an open task follows its file when the daemon moves it to another status's
// folder, on a daemon serving a workspace on disk and its todo store there.

var alphaTodos = todo.At{Workspace: "alpha"}

// onTodoDisk is a TUI over a daemon whose workspace alpha is a folder on disk with a todo store,
// one task in it, open in the editor (its path in alpha returned); and the daemon's todo manager,
// to move the task as another client does.
func onTodoDisk(t *testing.T) (r *running, root string, m *todo.Manager, task todo.Task) {
	t.Helper()
	root = fileDir(t, "a.md", "one\n")
	// the manager closed once the daemon has stopped, before root is removed, as the daemon closes
	// its own at its end: a store left open keeps its folder in use, which Windows won't remove
	t.Cleanup(func() {
		if m != nil {
			_ = m.Close()
		}
	})
	d := startManagedIncludes(t, map[string]string{"alpha": root}, nil, serving.Options{}, serving.Supplied{},
		func(ws *serving.Workspaces) module.Module {
			m = todo.New(serving.TodoRoots{Workspaces: ws}, serving.TodoDisk{}, todotest.NewJournal())
			return m
		})
	ctx := context.Background()
	if _, err := m.Scaffold(ctx, alphaTodos); err != nil {
		t.Fatal(err)
	}
	task, err := m.Add(ctx, alphaTodos, todo.Spec{Title: "Follow me"}, "")
	if err != nil {
		t.Fatal(err)
	}
	r = runTUI(t, NewSession(d.sock, nil), Options{Workspace: "alpha"})
	r.s.WaitForText(t, "· alpha")
	r.prefsIn(t)
	r.openOnDisk(t, ".todo-list/"+task.Path)
	return r, root, m, task
}

// waitNoticed waits for a notice holding text to be kept: a toast's text can wrap on the screen.
func waitNoticed(t *testing.T, r *running, text string) {
	t.Helper()
	r.s.WaitFor(t, "the notice "+text, func(string) bool {
		return onLoop(r, func() bool {
			return slices.ContainsFunc(r.h.noticeLog.Items, func(n feature.Notice) bool { return strings.Contains(n.Text, text) })
		})
	})
}

// TestAnOpenTaskFollowsItsStatusMove: another client moves the open task to completed; the buffer
// takes the new path and the moved file's text, clean, as one undo step, with the cursor where it
// was, and no buffer is left on the old path. A save then writes the new path, never the old one.
func TestAnOpenTaskFollowsItsStatusMove(t *testing.T) {
	t.Parallel()
	r, root, m, task := onTodoDisk(t)
	from, to := ".todo-list/"+task.Path, ".todo-list/completed/"+task.ID+".md"
	// the cursor on a line of the body, below the frontmatter the move rewrites
	body := onLoop(r, func() int {
		return slices.IndexFunc(r.h.core.Lines(), func(l string) bool { return strings.Contains(l, "HAND-EDIT FREELY") })
	})
	onLoop(r, func() bool { r.h.core.SetLine(body, 0); return true })
	moved, err := m.Status(context.Background(), alphaTodos, task.ID, task.Rev, "completed")
	if err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the buffer on "+to, func(string) bool { return r.file().path == to })
	want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(to)))
	if err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the moved file's text", func(string) bool { return r.editorText() == string(want) })
	if f := r.file(); f.dirty || f.disk != diskSame || f.version == "" {
		t.Fatalf("after the move: dirty %v, disk %v, version %q; want clean, as on disk", f.dirty, f.disk, f.version)
	}
	if !strings.Contains(r.editorText(), "status: completed") || moved.Path != "completed/"+task.ID+".md" {
		t.Fatalf("the buffer does not hold the completed task: %q", r.editorText())
	}
	if line := onLoop(r, func() string { return r.h.core.Lines()[r.h.core.Cursor().Line] }); !strings.Contains(line, "HAND-EDIT FREELY") {
		t.Errorf("the cursor is on %q, want the line it was on", line)
	}
	if b := onLoop(r, func() *openBuffer { return r.h.bufferOf(feature.FileRef{WS: "alpha", Path: from}) }); b != nil {
		t.Errorf("a buffer is still on the old path %s", from)
	}
	waitNoticed(t, r, from+" moved to "+to)

	r.typeInEditor(t, "X")
	r.waitTyped(t)
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saved "+to)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(from))); !os.IsNotExist(err) {
		t.Fatalf("the save wrote the task in its old folder too: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(to))); !strings.Contains(string(b), "X     HAND-EDIT FREELY") {
		t.Fatalf("the save did not write the moved file: %q", b)
	}
}

// TestAModifiedTaskFollowsItsMoveAndAsks: a task with unsaved edits follows its move too, its edits
// kept and badged changed on disk; its save asks rather than writing over the moved file, and
// nothing is written in the old folder.
func TestAModifiedTaskFollowsItsMoveAndAsks(t *testing.T) {
	t.Parallel()
	r, root, m, task := onTodoDisk(t)
	from, to := ".todo-list/"+task.Path, ".todo-list/deferred/"+task.ID+".md"
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	if _, err := m.Status(context.Background(), alphaTodos, task.ID, task.Rev, "deferred"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the buffer on "+to, func(string) bool { return r.file().path == to })
	if f := r.file(); !f.dirty || f.disk != diskChanged || !strings.HasPrefix(r.editorText(), "X") {
		t.Fatalf("after the move: dirty %v, disk %v, text %q; want the edits kept, badged changed", f.dirty, f.disk, r.editorText())
	}
	waitNoticed(t, r, from+" moved to "+to)
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "file changed on disk") // the question: keep, reload or overwrite
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(from))); !os.IsNotExist(err) {
		t.Fatalf("the save wrote the task in its old folder: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(to))); strings.HasPrefix(string(b), "X") {
		t.Fatal("the save wrote over the moved file without asking")
	}
}

// TestAFileThatIsNoTaskIsStillDeleted: a Markdown file deleted on disk that no store holds is badged
// deleted, as before: following a move is for a store's tasks alone.
func TestAFileThatIsNoTaskIsStillDeleted(t *testing.T) {
	t.Parallel()
	r, root, _, _ := onTodoDisk(t)
	r.openOnDisk(t, "a.md")
	if err := os.Remove(filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "a.md badged deleted", func(string) bool { return diskOf(r, "a.md") == diskDeleted })
	if p := r.file().path; p != "a.md" {
		t.Fatalf("the deleted file's buffer moved to %s", p)
	}
}

// TestATaskMovedWhileUnwatchedIsFollowedOnANewConnection: a move the disk watch did not see (the
// daemon away, say) is found by the check every open buffer gets on a new connection, which would
// otherwise close a clean buffer whose file is gone: the buffer follows the task instead.
func TestATaskMovedWhileUnwatchedIsFollowedOnANewConnection(t *testing.T) {
	t.Parallel()
	r, _, m, task := onTodoDisk(t)
	to := ".todo-list/completed/" + task.ID + ".md"
	onLoop(r, func() bool { r.h.unwatchFile(r.h.active); return true }) // no event will say it moved
	if _, err := m.Status(context.Background(), alphaTodos, task.ID, task.Rev, "completed"); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(r.h.recheckFile)
	r.s.WaitFor(t, "the buffer on "+to, func(string) bool { return r.file().path == to })
	if f := r.file(); !f.open || f.dirty || !strings.Contains(r.editorText(), "status: completed") {
		t.Fatalf("after the check: open %v, dirty %v, text %q; want the completed task, clean", f.open, f.dirty, r.editorText())
	}
	waitNoticed(t, r, " moved to "+to)
}

// TestARemovedTasksCleanBufferCloses: another client removes the open task; its buffer, with no
// unsaved edits, closes rather than staying badged deleted, a save away from writing the task back.
func TestARemovedTasksCleanBufferCloses(t *testing.T) {
	t.Parallel()
	r, root, m, task := onTodoDisk(t)
	from := ".todo-list/" + task.Path
	if err := m.Remove(context.Background(), alphaTodos, task.ID, task.Rev); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "no buffer on "+from, func(string) bool {
		return onLoop(r, func() bool { return r.h.bufferOf(feature.FileRef{WS: "alpha", Path: from}) == nil })
	})
	waitNoticed(t, r, "task was removed")
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(from))); !os.IsNotExist(err) {
		t.Fatalf("the removed task's file is back: %v", err)
	}
}

// TestARemovedTasksModifiedBufferKeepsItsEditsAndRefusesItsSave: a removed task's buffer with
// unsaved edits keeps them, badged deleted on disk (editor.document says so too); its save is
// refused, and nothing is written: the task never comes back from a buffer. A later check of the
// buffer says nothing more.
func TestARemovedTasksModifiedBufferKeepsItsEditsAndRefusesItsSave(t *testing.T) {
	t.Parallel()
	r, root, m, task := onTodoDisk(t)
	from := ".todo-list/" + task.Path
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	if err := m.Remove(context.Background(), alphaTodos, task.ID, task.Rev); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the buffer settled as its task's gone", func(string) bool { return r.file().taskGone })
	if f := r.file(); f.path != from || !f.dirty || f.disk != diskDeleted || !strings.HasPrefix(r.editorText(), "X") {
		t.Fatalf("after the removal: %s, dirty %v, disk %v, text %q; want its edits kept, badged deleted", f.path, f.dirty, f.disk, r.editorText())
	}
	waitNoticed(t, r, "task was removed")
	doc := onLoop(r, func() map[string]any { d, _ := r.h.editorDocument("", nil); m, _ := d.(map[string]any); return m })
	if doc["disk"] != "deleted" {
		t.Errorf("editor.document says disk %v, want deleted", doc["disk"])
	}
	r.keys(t, decltest.Ctrl('s'))
	waitNoticed(t, r, from+" is not saved")
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(from))); !os.IsNotExist(err) {
		t.Fatalf("the save wrote the removed task back: %v", err)
	}
	notices := onLoop(r, func() int { return len(r.h.noticeLog.Items) })
	onLoop(r, func() bool { r.h.diskSoon(r.h.active); return true })
	time.Sleep(4 * diskWait) // NOTE: absence: no event says a check found nothing to say
	if n := onLoop(r, func() int { return len(r.h.noticeLog.Items) }); n != notices {
		t.Errorf("a later check said %d notices more, want none", n-notices)
	}
}

// TestAHandMovedTaskIsFollowedByItsID: a task's file moved by hand (no change the window read
// names it) is found by its id: the buffer follows it.
func TestAHandMovedTaskIsFollowedByItsID(t *testing.T) {
	t.Parallel()
	r, root, _, task := onTodoDisk(t)
	from, to := ".todo-list/"+task.Path, ".todo-list/deferred/"+task.ID+".md"
	if err := os.MkdirAll(filepath.Join(root, ".todo-list", "deferred"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, filepath.FromSlash(from)), filepath.Join(root, filepath.FromSlash(to))); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the buffer on "+to, func(string) bool { return r.file().path == to })
	if f := r.file(); f.dirty || f.disk != diskSame || f.taskGone {
		t.Fatalf("after the hand move: dirty %v, disk %v, gone %v; want clean, as on disk", f.dirty, f.disk, f.taskGone)
	}
	waitNoticed(t, r, from+" moved to "+to)
}

// TestAMoveTheFeedReadIsFollowedWithoutTheDiskWatch: the todo feed's move of a file asks for a check
// of the buffer on it at once, the disk watch silent, and names where it went.
func TestAMoveTheFeedReadIsFollowedWithoutTheDiskWatch(t *testing.T) {
	t.Parallel()
	r, root, m, task := onTodoDisk(t)
	to := ".todo-list/completed/" + task.ID + ".md"
	onLoop(r, func() bool { r.h.unwatchFile(r.h.active); return true }) // no event will say it moved
	moved, err := m.Status(context.Background(), alphaTodos, task.ID, task.Rev, "completed")
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, ".todo-list")
	r.h.p.Post(func() { r.h.todoFilesChanged([]todoFileChange{{store: store, from: task.Path, to: moved.Path}}) })
	r.s.WaitFor(t, "the buffer on "+to, func(string) bool { return r.file().path == to })
	if f := r.file(); f.dirty || !strings.Contains(r.editorText(), "status: completed") {
		t.Fatalf("after the feed's move: dirty %v, text %q; want the completed task, clean", f.dirty, r.editorText())
	}
}

// TestAnotherStoresMoveIsNotFollowed: the feed reads several stores, and a move is its own store's.
// A move of the same relative path in another store, read later, names a file this store holds too
// (an unrelated document at deferred/<id>.md): the buffer follows its own store's move, never the
// other's (the mutant: the hint matched by its relative path alone).
func TestAnotherStoresMoveIsNotFollowed(t *testing.T) {
	t.Parallel()
	r, root, m, task := onTodoDisk(t)
	from, to := ".todo-list/"+task.Path, ".todo-list/completed/"+task.ID+".md"
	onLoop(r, func() bool { r.h.unwatchFile(r.h.active); return true }) // the feed alone says it moved
	moved, err := m.Status(context.Background(), alphaTodos, task.ID, task.Rev, "completed")
	if err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(root, ".todo-list", "deferred", task.ID+".md")
	if err := os.MkdirAll(filepath.Dir(decoy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("---\nid: \"notes\"\n---\n\n# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, other := filepath.Join(root, ".todo-list"), filepath.Join(t.TempDir(), ".todo-list")
	r.h.p.Post(func() {
		r.h.todoFilesChanged([]todoFileChange{{store: store, from: task.Path, to: moved.Path},
			{store: other, from: task.Path, to: "deferred/" + task.ID + ".md"}})
	})
	r.s.WaitFor(t, "the buffer to leave "+from, func(string) bool { return r.file().path != from })
	if got := r.file().path; got != to {
		t.Fatalf("the buffer followed to %s, want its own store's move, %s", got, to)
	}
}

// TestAMoveToAnotherTasksFileIsNotFollowed: a move hint is taken only when the file it names is the
// same task's, by its own id: one naming another task's file (a stale hint) is passed over, and the
// task is found by its id (the mutant: any readable file the hint names followed).
func TestAMoveToAnotherTasksFileIsNotFollowed(t *testing.T) {
	t.Parallel()
	r, root, m, task := onTodoDisk(t)
	from, to := ".todo-list/"+task.Path, ".todo-list/completed/"+task.ID+".md"
	ctx := context.Background()
	y, err := m.Add(ctx, alphaTodos, todo.Spec{Title: "Another"}, "")
	if err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.unwatchFile(r.h.active); return true })
	if _, err := m.Status(ctx, alphaTodos, task.ID, task.Rev, "completed"); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, ".todo-list")
	r.h.p.Post(func() { r.h.todoFilesChanged([]todoFileChange{{store: store, from: task.Path, to: y.Path}}) })
	r.s.WaitFor(t, "the buffer to leave "+from, func(string) bool { return r.file().path != from })
	if got := r.file().path; got != to {
		t.Fatalf("the buffer followed to %s, want the task's own file, %s", got, to)
	}
}
