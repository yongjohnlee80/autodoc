package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// outside_test.go: files outside every workspace
// (docs/decisions/editor.md#files-outside-every-workspace) — File › Open file… and File › New file…
// over the whole disk, file.locate's routing into a workspace, and what such a file keeps and
// lacks.

// diskFiles is the daemon's documents of the whole disk, as app/serve.go serves them.
func diskFiles(t *testing.T, opts ...docs.Option) *docs.Docs {
	t.Helper()
	disk, err := local.New("/")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	return docs.New(disk, nil, opts...)
}

// runOutside is the TUI on a managed daemon serving kb and notes from real folders, and a folder
// in neither holding notes.md; it answers the TUI, kb's root and the outside file.
func runOutside(t *testing.T) (r *running, kb, notes, abs string) {
	t.Helper()
	kb = fileDir(t, "a.md", "# A\n")
	notes = fileDir(t, "c.md", "# C\n")
	abs = filepath.Join(fileDir(t, "notes.md", "# Notes\n"), "notes.md")
	d := startManaged(t, map[string]string{"kb": kb, "notes": notes})
	r = runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	return r, kb, notes, abs
}

func (r *running) waitOpen(t *testing.T, ws, p string) {
	t.Helper()
	r.s.WaitFor(t, "file "+ws+":"+p+" open", func(string) bool {
		f := r.file()
		return f.open && f.ws == ws && f.path == p && !f.dirty
	})
}

func waitDisk(t *testing.T, r *running, abs, want string) {
	t.Helper()
	r.s.WaitFor(t, abs+" holding "+want, func(string) bool { b, _ := os.ReadFile(abs); return string(b) == want })
}

// TestAFileOutsideEveryWorkspaceOpensEditsAndSaves: it opens in no workspace, the status line
// says so, a save writes it, and a change on disk since it was read is a conflict that asks
// (docs/decisions/editor.md#files-outside-every-workspace).
func TestAFileOutsideEveryWorkspaceOpensEditsAndSaves(t *testing.T) {
	r, _, _, abs := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	r.s.WaitForText(t, "opened "+abs[:20]) // the status line's left, not left at "opening …"
	r.s.WaitForText(t, "["+outsideBadge+"]")
	if r.editorText() != "# Notes\n" {
		t.Fatalf("the page holds %q", r.editorText())
	}
	read := r.file().version
	r.typeInEditor(t, "more ")
	r.keys(t, decltest.Ctrl('s'))
	waitDisk(t, r, abs, "more # Notes\n")
	// The disk holds the save before the daemon has stat'ed the file for the version it answers:
	// a write from here before the TUI has that answer would be the version it answers, and the
	// next save would not conflict (vfs/local's version, "a foreign write racing the lstat").
	r.s.WaitFor(t, "the save's version adopted", func(string) bool {
		f := r.file()
		return !f.dirty && f.version != read
	})
	if err := os.WriteFile(abs, []byte("# Changed elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.typeInEditor(t, "x")
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "changed on disk since you opened it")
	if b, _ := os.ReadFile(abs); string(b) != "# Changed elsewhere\n" {
		t.Errorf("a stale save wrote: %q", b)
	}
}

// TestOpenFileInAWorkspacesRootOpensItThere: a file a workspace indexes opens as that workspace's,
// entering it when it is not the one in use
// (docs/decisions/editor.md#files-outside-every-workspace).
func TestOpenFileInAWorkspacesRootOpensItThere(t *testing.T) {
	r, kb, notes, _ := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(notes, "c.md")) })
	r.waitOpen(t, "notes", "c.md")
	if ws := onLoop(r, func() string { return r.h.ws }); ws != "notes" {
		t.Errorf("the workspace in use is %q, want notes", ws)
	}
}

// TestAnOutsideFileStaysOpenAcrossWorkspacesAndReachesTheFeedAndRecent: switching workspace keeps
// it; the plugin feed sends workspace "" and its absolute path; Recent files lists it as in no
// workspace and opens it again (docs/decisions/editor.md#files-outside-every-workspace).
func TestAnOutsideFileStaysOpenAcrossWorkspacesAndReachesTheFeedAndRecent(t *testing.T) {
	r, _, _, abs := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	r.h.p.Post(func() { r.h.enter("notes") })
	r.s.WaitFor(t, "notes in use", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "notes" && r.h.entered }) })
	r.waitOpen(t, "", abs)
	if d := onLoop(r, r.h.plugins.document); d.Workspace != "" || d.Path != abs || d.Text != "# Notes\n" {
		t.Errorf("the feed's document: workspace %q path %q text %q", d.Workspace, d.Path, d.Text)
	}
	r.h.p.Post(func() { r.h.closeFile() })
	r.s.WaitFor(t, "closed", func(string) bool { return !r.file().open })
	r.leader(t, 'r')
	r.s.WaitForText(t, "("+outsideBadge+")")
	rows := r.recentListedNow()
	if len(rows) == 0 || rows[0] != (recentDoc{Workspace: "", Path: abs}) {
		t.Fatalf("recent files %v, want the outside file first", rows)
	}
	r.h.p.Post(func() { r.h.recent.choose(0) })
	r.waitOpen(t, "", abs)
	if ws := onLoop(r, func() string { return r.h.ws }); ws != "notes" {
		t.Errorf("reopening it left the workspace in use %q, want notes", ws)
	}
}

// TestNewFileInAnyFolder: Ctrl+N's save dialog creates a file in a folder that does not exist yet,
// outside every workspace (".md" added); one named in a workspace's root is that workspace's file;
// one that exists is left as it is (docs/decisions/editor.md#files-outside-every-workspace).
func TestNewFileInAnyFolder(t *testing.T) {
	r, kb, _, abs := runOutside(t)
	fresh := filepath.Join(filepath.Dir(abs), "fresh", "idea")
	r.keys(t, decltest.Ctrl('n'))
	r.s.WaitForText(t, "new file")
	r.keys(t, decltest.Type(fresh)...)
	r.keys(t, enter())
	r.waitOpen(t, "", fresh+".md")
	if b, err := os.ReadFile(fresh + ".md"); err != nil || len(b) != 0 {
		t.Fatalf("the new file: %q, %v", b, err)
	}
	r.keys(t, decltest.Ctrl('n'))
	r.s.WaitForText(t, "new file")
	r.keys(t, decltest.Type(filepath.Join(kb, "made"))...)
	r.keys(t, enter())
	r.waitOpen(t, "kb", "made.md")
	r.keys(t, decltest.Ctrl('n'))
	r.s.WaitForText(t, "new file")
	r.keys(t, decltest.Type(abs)...)
	r.keys(t, enter())
	r.s.WaitForText(t, "exists")
	if b, _ := os.ReadFile(abs); string(b) != "# Notes\n" {
		t.Errorf("creating over a file changed it: %q", b)
	}
}

// TestOpenFileDialogOpensTheFileChosen: SPC f lists the disk at the workspace's root, with the
// preview, and Enter on a file opens it.
func TestOpenFileDialogOpensTheFileChosen(t *testing.T) {
	r, _, _, _ := runOutside(t)
	r.leader(t, 'f')
	r.s.WaitForText(t, "open a file")
	r.s.WaitForText(t, "a.md")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown}, enter()) // past ../
	r.waitOpen(t, "kb", "a.md")
}

// TestTheFileDialogsFollowALinkToAFolder: a link to a folder, absolute, lists as a folder in
// Open file…, and Enter goes into it.
func TestTheFileDialogsFollowALinkToAFolder(t *testing.T) {
	r, kb, _, abs := runOutside(t)
	if err := os.Symlink(filepath.Dir(abs), filepath.Join(kb, "linked")); err != nil {
		t.Fatal(err)
	}
	r.leader(t, 'f')
	r.s.WaitForText(t, "open a file")
	r.s.WaitForText(t, "linked/")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown}, enter()) // ../, linked/
	r.s.WaitForText(t, "notes.md")
}

// TestARefusedFileVerbSaysTheDaemonIsRemote: the daemon refuses file.* to a peer not on its unix
// socket; the TUI says why rather than echoing the refusal.
func TestARefusedFileVerbSaysTheDaemonIsRemote(t *testing.T) {
	denied := &golibrpc.Error{Code: golibrpc.CodeAccessDenied, Message: "file.read is for local peers"}
	if got := wireMessage(localOnly(denied)); !strings.Contains(got, "this AutoDoc is remote") {
		t.Errorf("a refusal reads %q", got)
	}
	other := &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: "bad"}
	if localOnly(other) != error(other) {
		t.Error("another error was rewritten")
	}
}

// TestReopeningTheOpenOutsideFileKeepsThePage: opening the file already open, unchanged, does not
// read it again.
func TestReopeningTheOpenOutsideFileKeepsThePage(t *testing.T) {
	r, _, _, abs := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	gen := r.file().gen
	// an open outside every workspace is where Open file… lands once file.locate answers; a load
	// would number a new open at once, on the loop
	if g := onLoop(r, func() uint64 { r.h.openIn("", abs); return r.h.file.gen }); g != gen {
		t.Errorf("reopening read the file again (gen %d -> %d)", gen, g)
	}
}

// TestAnOutsideFileGoneFromDiskIsClosedOnRecheck: a reconnect's recheck finds it deleted, and
// closes it, saying it is gone from disk (not "from the workspace").
func TestAnOutsideFileGoneFromDiskIsClosedOnRecheck(t *testing.T) {
	r, _, _, abs := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() { r.h.enter(r.h.ws) }) // a reconnect enters the workspace again
	r.s.WaitFor(t, "closed", func(string) bool { return !r.file().open })
	r.waitKeptNotice(t, abs+" is gone from disk: closed")
}

// TestTheDefaultAppOpensAnOutsideFileAtItsPath: Open with Default App hands the desktop the absolute path, and
// says so when the desktop cannot open it.
func TestTheDefaultAppOpensAnOutsideFileAtItsPath(t *testing.T) {
	r, _, _, abs := runOutside(t)
	opened := make(chan string, 2)
	// set on the loop, read by the browser stub on openWithDefaultApp's worker: atomic
	var fail atomic.Bool
	r.h.p.Post(func() {
		r.h.browser = func(_ context.Context, p string) error {
			opened <- p
			if fail.Load() {
				return errors.New("no viewer")
			}
			return nil
		}
		r.h.openAbsolute(abs)
	})
	r.waitOpen(t, "", abs)
	r.h.p.Post(r.h.openWithDefaultApp)
	if p := <-opened; p != abs {
		t.Errorf("the viewer was given %q, want %q", p, abs)
	}
	r.h.p.Post(func() { fail.Store(true); r.h.openWithDefaultApp() })
	<-opened
	r.waitKeptNotice(t, "open with the default app: no viewer")
}

// waitKeptNotice waits for text in the notifications' history (SPC h): a toast with a long path
// wraps on the screen.
func (r *running) waitKeptNotice(t *testing.T, text string) {
	t.Helper()
	r.s.WaitFor(t, "the notice "+text, func(string) bool {
		return onLoop(r, func() bool {
			for _, n := range r.h.noticeLog.items {
				if n.text == text {
					return true
				}
			}
			return false
		})
	})
}

// whyOutside (docs/decisions/todos.md#excluded-todo-stores-are-reported): the innermost workspace
// root holding a file, and the exclude pattern that keeps it out ("" when it is only not admitted);
// none for a file under no root.
func TestWhyAFileIsInNoWorkspace(t *testing.T) {
	list := []wsInfo{
		{name: "outer", root: "/src", exclude: []string{".git/**"}},
		{name: "kb", root: "/src/kb", exclude: []string{".git/**", ".todo-list/**"}},
	}
	for _, c := range []struct {
		abs, ws, pattern string
		ok               bool
	}{
		{"/src/kb/.todo-list/open/t.md", "kb", ".todo-list/**", true},
		{"/src/kb/notes/x.go", "kb", "", true},
		{"/src/other/a.md", "outer", "", true},
		{"/elsewhere/a.md", "", "", false},
		{"/src/kbx/a.md", "outer", "", true}, // a sibling sharing the prefix is not under kb
	} {
		w, p, ok := whyOutside(list, c.abs)
		if ok != c.ok || w.name != c.ws || p != c.pattern {
			t.Errorf("%s: %q %q %v; want %q %q %v", c.abs, w.name, p, ok, c.ws, c.pattern, c.ok)
		}
	}
}

// A file a workspace's exclude keeps out, opened by its path, says which workspace and which
// pattern, and offers that workspace's settings
// (docs/decisions/todos.md#excluded-todo-stores-are-reported).
func TestAnExcludedFileSaysWhyAndOffersItsSettings(t *testing.T) {
	root := fileDir(t, "a.md", "# A\n")
	if err := os.MkdirAll(filepath.Join(root, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "drafts", "d.md"), []byte("# D\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := startManaged(t, map[string]string{"kb": root})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 0)
	r.saveForm(func(f *settingsForm) { f.exclude = ".git/**; drafts/**" })
	r.waitNoticed(t, "kb: saved")
	r.s.WaitFor(t, "the listing has the exclude", func(string) bool {
		return onLoop(r, func() bool {
			w, ok := r.h.workspaces.At(0)
			return ok && slices.Contains(w.exclude, "drafts/**")
		})
	})
	// over unsaved changes nothing is asked about them: the file opens in a buffer of its own, a.md
	// keeps its edit in its, and the excluded-file question follows the open
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "x")
	r.s.WaitFor(t, "a.md unsaved", func(sc string) bool { return strings.Contains(sc, "a.md [+]") })
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(root, "drafts", "d.md")) })
	r.s.WaitForText(t, "in no workspace: open kb's settings?")
	r.s.WaitForText(t, "keeps it out")
	r.keys(t, key('o'))
	r.s.WaitForText(t, "workspace settings · kb")
	if strings.Contains(r.s.String(), "unsaved file") {
		t.Fatal("opening the excluded file asked about a.md's unsaved changes")
	}
	if a := onLoop(r, func() *openBuffer { return r.h.bufferOf(fileRef{"kb", "a.md"}) }); a == nil || !onLoop(r, func() bool { return a.file.dirty }) {
		t.Fatal("a.md's buffer lost its unsaved edit")
	}
}

// A feature reads and writes a file outside every workspace through core (the host's readFile and
// writeFile): created from no version, read back with the version it wrote, a stale version a
// conflict.
func TestCoreReadsAndWritesAnAbsoluteFile(t *testing.T) {
	r, _, _, abs := runOutside(t)
	c := core{r.h}
	p := filepath.Join(filepath.Dir(abs), ".autodoc", "graph.toml")
	ctx := context.Background()
	v1, err := c.WriteAbsolute(ctx, p, []byte("one\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, v, err := c.ReadAbsolute(ctx, p)
	if err != nil || string(b) != "one\n" || v != v1 {
		t.Fatalf("read back %q, %q, %v; want one, %q", b, v, err, v1)
	}
	if _, err := c.WriteAbsolute(ctx, p, []byte("two\n"), "stale"); code(err) != rpc.CodeConflict {
		t.Errorf("a stale version: %v; want a conflict", err)
	}
	if _, _, err := c.ReadAbsolute(ctx, filepath.Join(filepath.Dir(abs), "none.toml")); code(err) != rpc.CodeNotFound {
		t.Errorf("no file: %v; want not found", err)
	}
	// the theme choices a chooser offers: its own first, then every theme the window can wear
	names, labels := themeChoices(c, []string{"terminal", "page"}, []string{"the terminals'", "the page's"})
	if names[0] != "terminal" || labels[1] != "the page's" || len(names) != len(labels) || len(names) < 3 {
		t.Errorf("choices %q, %q", names, labels)
	}
}
