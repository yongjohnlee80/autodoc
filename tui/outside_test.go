package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/docs"
)

// outside_test.go: files outside every workspace (ADR 1791430651) — File › Open file… and File ›
// New file… over the whole disk, file.locate's routing into a workspace, and what such a file keeps
// and lacks.

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
// (ADR 1791430651 §5.1, §5.7).
func TestAFileOutsideEveryWorkspaceOpensEditsAndSaves(t *testing.T) {
	r, _, _, abs := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	r.s.WaitForText(t, "opened "+abs[:20]) // the status line's left, not left at "opening …"
	r.s.WaitForText(t, "["+outsideBadge+"]")
	if r.editorText() != "# Notes\n" {
		t.Fatalf("the page holds %q", r.editorText())
	}
	r.typeInEditor(t, "more ")
	r.keys(t, decltest.Ctrl('s'))
	waitDisk(t, r, abs, "more # Notes\n")
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
// entering it when it is not the one in use (§5.4).
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
// workspace and opens it again (§5.6).
func TestAnOutsideFileStaysOpenAcrossWorkspacesAndReachesTheFeedAndRecent(t *testing.T) {
	r, _, _, abs := runOutside(t)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	r.h.p.Post(func() { r.h.enter("notes") })
	r.s.WaitFor(t, "notes in use", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "notes" && r.h.entered }) })
	r.waitOpen(t, "", abs)
	if d := onLoop(r, r.h.document); d.Workspace != "" || d.Path != abs || d.Text != "# Notes\n" {
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
	r.h.p.Post(func() { r.h.recentSelect(0) })
	r.waitOpen(t, "", abs)
	if ws := onLoop(r, func() string { return r.h.ws }); ws != "notes" {
		t.Errorf("reopening it left the workspace in use %q, want notes", ws)
	}
}

// TestNewFileInAnyFolder: Ctrl+N's save dialog creates a file in a folder that does not exist yet,
// outside every workspace (".md" added); one named in a workspace's root is that workspace's
// file; one that exists is left as it is (§5.2).
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
	// openOutside is where Open file… lands once file.locate answers; a load would number a new
	// open at once, on the loop
	if g := onLoop(r, func() uint64 { r.h.openOutside(abs); return r.h.file.gen }); g != gen {
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
	fail := false
	r.h.p.Post(func() {
		r.h.browser = func(_ context.Context, p string) error {
			opened <- p
			if fail {
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
	r.h.p.Post(func() { fail = true; r.h.openWithDefaultApp() })
	<-opened
	r.waitKeptNotice(t, "open with the default app: no viewer")
}

// waitKeptNotice waits for text in the notifications' history (SPC h): a toast with a long path
// wraps on the screen.
func (r *running) waitKeptNotice(t *testing.T, text string) {
	t.Helper()
	r.s.WaitFor(t, "the notice "+text, func(string) bool {
		return onLoop(r, func() bool {
			for _, n := range r.h.notices {
				if n.text == text {
					return true
				}
			}
			return false
		})
	})
}
