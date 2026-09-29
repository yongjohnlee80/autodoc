package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
	"github.com/yongjohnlee80/golib/tui/widget"
	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

func md(p string) bool { return strings.HasSuffix(p, ".md") }

// committing is memfs whose conditional writes land, then report a failure after the commit point.
type committing struct{ *memfs.FS }

func (c committing) WriteFileIf(ctx context.Context, name string, r io.Reader, want vfs.Version, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	fi, err := c.FS.WriteFileIf(ctx, name, r, want, opts...)
	if err != nil {
		return fi, err
	}
	return vfs.FileInfo{}, &vfs.CommitError{Path: name, Info: fi, Err: errors.New("fsync: input/output error")}
}

// daemon is a real autodoc daemon for the TUI to talk to: the RPC server over the core, on a unix
// socket, with its workspaces on memfs so a test can change the files underneath.
type daemon struct {
	sock string
	fs   map[string]*memfs.FS
	db   *store.Store
	stop func()
}

// startDaemon serves the workspaces named (each with its notes, path then content). A name ending
// in "!" is served on a filesystem whose writes land and then fail (Committed).
func startDaemon(t *testing.T, workspaces map[string][]string) *daemon {
	t.Helper()
	return startDaemonOn(t, "", workspaces)
}

// startDaemonOn is startDaemon on the socket sock ("" for a new one).
func startDaemonOn(t *testing.T, sock string, workspaces map[string][]string) *daemon {
	t.Helper()
	return startDaemonWith(t, sock, workspaces, daemonOpts{})
}

// daemonOpts change the daemon: slow slows it down (each file read waits slow, with one indexing
// worker, and the daemon serves without waiting for the first scan); prefs are the preferences its
// store starts with, nil for the tests' own (the status line shown, which most tests read); emb
// serves embedding providers.
type daemonOpts struct {
	slow  time.Duration
	prefs map[string]string
	emb   rpc.Embeddings
}

// testPrefs are the preferences a test's store starts with: the status line shown, since it says
// what the TUI did.
var testPrefs = map[string]string{"tui.status.shown": "true"}

// slowFS is memfs whose reads wait.
type slowFS struct {
	*memfs.FS
	d time.Duration
}

func (f slowFS) Open(ctx context.Context, name string, offset int64) (io.ReadCloser, error) {
	time.Sleep(f.d)
	return f.FS.Open(ctx, name, offset)
}

func startDaemonWith(t *testing.T, sock string, workspaces map[string][]string, o daemonOpts) *daemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := &daemon{fs: map[string]*memfs.FS{}}
	var served []*rpc.Workspace
	var cores sync.WaitGroup // the indexers and followers, stopped before the store closes
	// one store for the daemon's workspaces; closed after the indexers stop (cleanups run last first)
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	d.db = db
	prefs := o.prefs
	if prefs == nil {
		prefs = testPrefs
	}
	for k, v := range prefs {
		if err := db.SetPreference(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range sortedKeys(workspaces) {
		mem := memfs.New()
		var fsys vfs.FS = mem
		wsName := strings.TrimSuffix(name, "!")
		if wsName != name {
			fsys = committing{mem}
		}
		workers := 0
		if o.slow > 0 {
			fsys, workers = slowFS{mem, o.slow}, 1
		}
		d.fs[wsName] = mem
		notes := workspaces[name]
		for i := 0; i < len(notes); i += 2 {
			if dir := filepath.Dir(notes[i]); dir != "." {
				_ = mem.MkdirAll(ctx, dir)
			}
			if _, err := mem.WriteFile(ctx, notes[i], strings.NewReader(notes[i+1])); err != nil {
				t.Fatal(err)
			}
		}
		row, err := db.AddWorkspace(ctx, wsName, "/"+wsName, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ixs := index.Open(db, row.ID)
		ix := index.NewIndexer(ixs, fsys, index.Options{Match: md, BatchDelay: 5 * time.Millisecond, Workers: workers})
		f := follow.New(fsys, ix, ix, follow.Options{Match: md, PollInterval: 20 * time.Millisecond})
		ix.SetRescanner(f)
		cores.Go(func() { _ = ix.Run(ctx) })
		cores.Go(func() { _ = f.Run(ctx) })
		served = append(served, &rpc.Workspace{Name: wsName, Root: "/" + wsName, Index: ix, Docs: docs.New(fsys, md), Following: f.Status})
		// wait until the notes are indexed, so the first listing has them (a slow daemon does not)
		for deadline := time.Now().Add(10 * time.Second); o.slow == 0; time.Sleep(10 * time.Millisecond) {
			st, _ := ixs.Status(ctx)
			if st.Docs == int64(len(notes)/2) && st.PendingJobs == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("workspace %s did not index", wsName)
			}
		}
	}
	d.sock = sock
	if sock == "" {
		dir, err := os.MkdirTemp("", "adt")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		d.sock = filepath.Join(dir, "s.sock")
	}
	ln, err := net.Listen("unix", d.sock)
	if err != nil {
		t.Fatal(err)
	}
	opts := []rpc.Option{rpc.WithListener(ln), rpc.WithPreferences(db)}
	if o.emb != nil {
		opts = append(opts, rpc.WithEmbeddings(o.emb))
	}
	srv := rpc.New(rpc.Fixed(served...), "v-test", opts...)
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	d.stop = func() { cancel(); <-done; cores.Wait() }
	t.Cleanup(func() { cancel(); <-done; cores.Wait() }) // before the store closes (its cleanup runs after)
	return d
}

func sortedKeys(m map[string][]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (d *daemon) read(t *testing.T, ws, p string) string {
	t.Helper()
	r, err := d.fs[ws].Open(context.Background(), p, 0)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func (d *daemon) write(t *testing.T, ws, p, s string) {
	t.Helper()
	if _, err := d.fs[ws].WriteFile(context.Background(), p, strings.NewReader(s)); err != nil {
		t.Fatal(err)
	}
}

// running is the TUI under test: its host and its screen.
type running struct {
	h *Host
	s *decltest.Screen
}

func runTUI(t *testing.T, sess *Session, opt Options) *running {
	t.Helper()
	h := newHost(sess, opt)
	s := decltest.RunWith(t, 100, 30, h.attach, h.options(opt)...)
	t.Cleanup(h.cancel)
	return &running{h: h, s: s}
}

// attached runs the TUI on d's socket, and waits for the workspace's notes to be listed.
func attached(t *testing.T, d *daemon) *running {
	t.Helper()
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	return r
}

// listed is the workspace's notes, as the pickers filter them.
func (r *running) listed() []string {
	return onLoop(r, func() []string { return append([]string(nil), r.h.notesAll...) })
}

// waitListed waits for the workspace's notes to be the n listed.
func (r *running) waitListed(t *testing.T, n int) {
	t.Helper()
	r.s.WaitFor(t, fmt.Sprintf("%d notes listed", n), func(string) bool { return len(r.listed()) == n })
}

// leader is SPC then k, from the page in Normal mode.
func (r *running) leader(t *testing.T, k rune) {
	t.Helper()
	r.keys(t, key(' '))
	r.s.WaitForText(t, "SPC — commands")
	r.keys(t, key(k))
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "SPC — commands") })
}

func (r *running) keys(t *testing.T, evs ...tuicore.Event) { t.Helper(); r.s.Keys(t, evs...) }

func esc() tuicore.Event   { return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape} }
func enter() tuicore.Event { return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter} }
func key(r rune) tuicore.Event {
	return decltest.Rune(r)
}

// typeInEditor types text in Insert mode and goes back to Normal.
func (r *running) typeInEditor(t *testing.T, text string) {
	t.Helper()
	r.keys(t, decltest.Alt('2'), key('i'))
	r.keys(t, decltest.Type(text)...)
	r.keys(t, esc())
}

// openByPicker opens a note through File › Open.
func (r *running) openByPicker(t *testing.T, p string) {
	t.Helper()
	r.keys(t, decltest.Ctrl('o'))
	r.s.WaitForText(t, "open a note")
	r.keys(t, decltest.Type(p)...)
	r.s.WaitFor(t, "the filter applied", func(sc string) bool { return strings.Contains(sc, "1 of ") })
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab}, enter())
}

// onLoop reads the host's state where it lives: on the UI loop.
func onLoop[T any](r *running, read func() T) T {
	ch := make(chan T, 1)
	r.h.p.Post(func() { ch <- read() })
	return <-ch
}

func (r *running) note() note         { return onLoop(r, func() note { return r.h.note }) }
func (r *running) editorText() string { return onLoop(r, r.h.editor.Value) }

func (r *running) waitNote(t *testing.T, p string) {
	t.Helper()
	r.s.WaitFor(t, "note "+p+" open", func(string) bool { n := r.note(); return n.open && n.path == p && !n.dirty })
	r.s.WaitForText(t, "opened "+p)
}

func TestEveryQMLFileIsSound(t *testing.T) {
	decltest.Check(t, newHost(NewSession("unused", nil), Options{}).options(Options{})...)
}

// TestSearchesOpensEditsSaves: the search picker finds as the words are typed, its hits on the
// left and the note under the cursor on the right; Enter opens the hit in the editor, the cursor
// at its section; typed text marks it unsaved; Ctrl+S writes it to the file.
func TestSearchesOpensEditsSaves(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n\nintro\n\n## Birds\n\nkestrel notes\n", "b/c.md", "# C\n\nplover\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.keys(t, decltest.Ctrl('g'))
	r.s.WaitForText(t, "words; a * ends a prefix")
	r.keys(t, decltest.Type("kestrel")...) // no Enter: the search runs as it is typed
	r.s.WaitFor(t, "the hit, previewed", func(sc string) bool {
		return strings.Contains(sc, "hits (1)") && strings.Contains(sc, "semantic off") && strings.Contains(sc, "kestrel notes") &&
			!strings.Contains(sc, "b/c.md")
	})
	r.keys(t, enter())
	r.waitNote(t, "a.md")
	// the hit is the Birds section's text: the cursor opens on it, not on the note's first line
	if line := onLoop(r, func() string { l, _ := r.h.editor.Line(); return r.h.editor.Lines()[l] }); line != "kestrel notes" {
		t.Errorf("the cursor opened on %q, not on the hit", line)
	}
	r.typeInEditor(t, "EDITED ")
	r.s.WaitFor(t, "unsaved mark", func(sc string) bool { return strings.Contains(sc, "a.md [+]") })
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saved a.md")
	if got := d.read(t, "kb", "a.md"); !strings.Contains(got, "EDITED ") || !strings.Contains(got, "kestrel notes") {
		t.Errorf("the file after the save: %q", got)
	}
	r.s.WaitFor(t, "the mark gone", func(sc string) bool { return !strings.Contains(sc, "[+]") })
}

// TestThePickerKeepsTheFileName: a path too long for the open picker's list loses its start, not
// its file name.
func TestThePickerKeepsTheFileName(t *testing.T) {
	deep := "archive/2026/09/projects/autodoc/reviews/2026-09-29-the-review-of-the-store.md"
	d := startDaemon(t, map[string][]string{"kb": {deep, "# R\n\nreview\n"}})
	r := attached(t, d)
	r.keys(t, decltest.Ctrl('o'))
	r.s.WaitFor(t, "the note's file name", func(sc string) bool {
		return strings.Contains(sc, "notes (1 of 1)") && strings.Contains(sc, "the-store.md")
	})
	// the list's row: the one holding the file name and not the preview's title
	for _, line := range strings.Split(r.s.String(), "\n") {
		if strings.Contains(line, "the-store.md") && strings.Contains(line, "archive/2026") && !strings.Contains(line, "┌") {
			t.Fatalf("the list kept the path's start and not its end:\n%s", r.s)
		}
	}
}

// TestAConflictAsks: a note changed on disk since it was opened is not overwritten: the save asks,
// and keep, reload and overwrite each do what they say.
func TestAConflictAsks(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	conflict := func() {
		t.Helper()
		r.keys(t, decltest.Ctrl('s'))
		r.s.WaitForText(t, "note changed on disk")
	}
	// keep: the edits stay, unsaved, and the disk is untouched
	d.write(t, "kb", "a.md", "theirs\n")
	r.typeInEditor(t, "mine ")
	conflict()
	r.keys(t, key('k'))
	r.s.WaitForText(t, "kept your changes")
	if got := d.read(t, "kb", "a.md"); got != "theirs\n" || !r.note().dirty {
		t.Fatalf("after keep: disk %q, dirty %v", got, r.note().dirty)
	}
	// overwrite: the disk gets mine
	conflict()
	r.keys(t, key('o'))
	r.s.WaitForText(t, "saved a.md")
	if got := d.read(t, "kb", "a.md"); got != "mine one\n" {
		t.Fatalf("after overwrite: disk %q", got)
	}
	// reload: the editor gets the disk's, and mine is gone
	d.write(t, "kb", "a.md", "theirs again\n")
	r.typeInEditor(t, "lost ")
	conflict()
	r.keys(t, key('r'))
	r.waitNote(t, "a.md")
	if got := r.editorText(); got != "theirs again\n" {
		t.Errorf("after reload: editor %q", got)
	}
}

// TestUnsavedWorkIsNeverLostQuietly: opening another note over unsaved changes asks; stay keeps
// them, discard drops them, save writes them first.
func TestUnsavedWorkIsNeverLostQuietly(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	r.typeInEditor(t, "X")
	ask := func() {
		t.Helper()
		r.openByPicker(t, "b.md")
		r.s.WaitForText(t, "unsaved note")
	}
	ask()
	r.keys(t, key('t'))
	r.s.WaitFor(t, "still a.md, unsaved", func(sc string) bool { return strings.Contains(sc, "a.md [+]") && !strings.Contains(sc, "unsaved note") })
	ask()
	r.keys(t, key('s'))
	r.waitNote(t, "b.md")
	if got := d.read(t, "kb", "a.md"); got != "Xaaa\n" {
		t.Errorf("save, then open: a.md %q", got)
	}
	r.typeInEditor(t, "Y")
	r.openByPicker(t, "a.md")
	r.s.WaitForText(t, "unsaved note")
	r.keys(t, key('d'))
	r.waitNote(t, "a.md")
	if got := d.read(t, "kb", "b.md"); got != "bbb\n" {
		t.Errorf("discard wrote b.md: %q", got)
	}
}

// TestQuitOverUnsavedAsks: quitting with unsaved changes asks; with none it quits at once.
func TestQuitOverUnsavedAsks(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	r.typeInEditor(t, "X")
	r.keys(t, decltest.Ctrl('q'))
	r.s.WaitForText(t, "quit autodoc?")
	r.keys(t, key('n'))
	r.s.WaitFor(t, "stayed", func(sc string) bool { return !strings.Contains(sc, "quit autodoc?") })
	r.keys(t, decltest.Ctrl('q'))
	r.s.WaitForText(t, "quit autodoc?")
	r.keys(t, key('y'))
	select {
	case <-r.s.Quit():
	case <-time.After(3 * time.Second):
		t.Fatal("did not quit")
	}
}

// TestNewNote: Ctrl+N names a note, which is created (".md" added) and opened; an existing name asks
// again, saying so.
func TestNewNote(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.keys(t, decltest.Ctrl('n'))
	r.s.WaitForText(t, "new note")
	r.keys(t, decltest.Type("fresh/idea")...)
	r.keys(t, enter())
	r.waitNote(t, "fresh/idea.md")
	if got := d.read(t, "kb", "fresh/idea.md"); got != "" {
		t.Errorf("the new note holds %q", got)
	}
	r.keys(t, decltest.Ctrl('n'))
	r.s.WaitForText(t, "new note")
	r.keys(t, decltest.Type("a")...)
	r.keys(t, enter())
	r.s.WaitForText(t, "a.md exists")
}

// TestCommittedIsReadBack: a save the daemon reports as landed-then-failed is read back, and adopted
// when the disk holds what was written.
func TestCommittedIsReadBack(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb!": {"a.md", "one\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	r.typeInEditor(t, "two ")
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saved a.md")
	if r.note().dirty || d.read(t, "kb", "a.md") != "two one\n" {
		t.Fatalf("dirty %v, disk %q", r.note().dirty, d.read(t, "kb", "a.md"))
	}
	// the adopted version is the disk's: the next save is not a conflict
	r.typeInEditor(t, "three ")
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitFor(t, "the second save", func(sc string) bool { return strings.Contains(d.read(t, "kb", "a.md"), "three") })
	if strings.Contains(r.s.String(), "note changed on disk") {
		t.Error("the adopted version conflicted")
	}
}

// TestBacklinks: SPC l opens the links panel over the notes linking to the open one, and Enter
// opens one, closing the panel.
func TestBacklinks(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"target.md", "# Target\n", "src.md", "see [[target]]\n"}})
	r := attached(t, d)
	r.openByPicker(t, "target.md")
	r.waitNote(t, "target.md")
	r.leader(t, 'l')
	r.s.WaitFor(t, "the backlink", func(sc string) bool { return strings.Contains(sc, "backlinks (1)") && strings.Contains(sc, "src.md") })
	r.keys(t, enter())
	r.waitNote(t, "src.md")
	r.s.WaitFor(t, "the panel closed", func(sc string) bool { return !strings.Contains(sc, "backlinks (") })
}

// TestWorkspaces: the picker lists the daemon's workspaces, and switching lists the other's notes.
func TestWorkspaces(t *testing.T) {
	d := startDaemon(t, map[string][]string{"alpha": {"a.md", "a\n"}, "beta": {"b1.md", "b\n", "b2.md", "b\n"}})
	sess := NewSession(d.sock, nil)
	release := make(chan struct{})
	sess.beforeCall = func(method string, params []any) {
		if method == "index.list" && len(params) > 0 && params[0] == "beta" {
			<-release
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· alpha")
	r.waitListed(t, 1)
	r.keys(t, decltest.Ctrl('w'))
	r.s.WaitForText(t, "beta")
	r.keys(t, key('j'), enter())
	r.s.WaitForText(t, "· beta")
	// beta's notes not come yet: the pickers have none, not alpha's
	if got := r.listed(); len(got) != 0 {
		t.Fatalf("in beta, before its notes came, the pickers had %v", got)
	}
	close(release)
	r.waitListed(t, 2)
	if got := r.listed(); got[1] != "b2.md" {
		t.Errorf("beta's notes: %v", got)
	}
}

// TestConnectFailedSaysSo: with nothing on the socket and no daemon to start, the TUI says so.
func TestConnectFailedSaysSo(t *testing.T) {
	sess := NewSession(filepath.Join(t.TempDir(), "none.sock"), nil)
	sess.window = 200 * time.Millisecond
	r := runTUI(t, sess, Options{})
	r.s.WaitFor(t, "the failure", func(sc string) bool {
		return strings.Contains(sc, "connect failed") && strings.Contains(sc, "[disconnected]")
	})
}

// TestSpawnsTheDaemonOnce: nothing answering, the TUI starts the daemon once, and attaches.
func TestSpawnsTheDaemonOnce(t *testing.T) {
	dir, err := os.MkdirTemp("", "ads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	// the daemon "spawned" is one already running elsewhere: spawning links the socket path to it,
	// after a while (as a real daemon takes time to come up), so the dial retries in between
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	var spawns atomic.Int32
	sess := NewSession(sock, func() (string, error) {
		spawns.Add(1)
		target := d.sock
		time.AfterFunc(300*time.Millisecond, func() { _ = os.Symlink(target, sock) })
		return "/state/serve.log", nil
	})
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.waitListed(t, 1)
	if n := spawns.Load(); n != 1 {
		t.Errorf("spawned %d times", n)
	}
}

// TestEveryThemeKeepsTextLegible: under each shipped theme the status line, the explorer and the
// note wear a foreground that differs from their background.
func TestEveryThemeKeepsTextLegible(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "legible text\n"}})
	for _, name := range themes.Names() {
		t.Run(name, func(t *testing.T) {
			src := themeImport.ReplaceAll(layout, []byte("import autodoc.theme."+name+" 1.0"))
			r := runTUI(t, NewSession(d.sock, nil), Options{Layout: src})
			r.s.WaitForText(t, "connected — autodoc v-test")
			r.openByPicker(t, "a.md")
			r.waitNote(t, "a.md")
			legible := func(text string) {
				t.Helper()
				x, y, ok := find(r.s.Backend.Snapshot(), text)
				if !ok {
					t.Fatalf("%q is not on screen:\n%s", text, r.s.String())
				}
				// the terminal's own foreground on its own background is legible (mono's documents);
				// any other pair must differ
				c := r.s.Backend.Snapshot()[y][x]
				terminals := c.Attrs.FG.Kind == tuicore.CellColorDefault && c.Attrs.BG.Kind == tuicore.CellColorDefault
				if !terminals && c.Attrs.FG == c.Attrs.BG && c.Attrs.Mask&tuicore.AttrReverse == 0 {
					t.Errorf("%q: foreground equals background (%+v)", text, c.Attrs)
				}
			}
			legible("legible text")
			legible("opened a.md")
			// the explorer, over the page's left
			r.leader(t, 'e')
			r.s.WaitForText(t, "explorer")
			legible("explorer")
			legible("kb")
		})
	}
}

// find is where text starts on a snapshot.
func find(cells [][]tuicore.Cell, text string) (int, int, bool) {
	for y, row := range cells {
		var b bytes.Buffer
		var xs []int
		for x, c := range row {
			if c.Content == "" {
				continue
			}
			for range len(c.Content) {
				xs = append(xs, x)
			}
			b.WriteString(c.Content)
		}
		if i := strings.Index(b.String(), text); i >= 0 {
			return xs[i], y, true
		}
	}
	return 0, 0, false
}

// TestASlowOpenNeverReplacesALaterOne: an open whose answer comes after a later open's is dropped:
// the editor holds the note opened last.
func TestASlowOpenNeverReplacesALaterOne(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"slow.md", "slow text\n", "fast.md", "fast text\n"}})
	sess := NewSession(d.sock, nil)
	release := make(chan struct{})
	sess.beforeCall = func(method string, params []any) {
		if method == "doc.read" && len(params) > 1 && params[1] == "slow.md" {
			<-release
		}
	}
	r := runTUI(t, sess, Options{})
	r.waitListed(t, 2)
	r.h.p.Post(func() { r.h.openPath("slow.md") })
	r.s.WaitForText(t, "opening slow.md")
	r.h.p.Post(func() { r.h.openPath("fast.md") })
	r.waitNote(t, "fast.md")
	close(release) // the slow answer lands now, after the later open
	time.Sleep(100 * time.Millisecond)
	if n, text := r.note(), r.editorText(); n.path != "fast.md" || text != "fast text\n" {
		t.Errorf("after the slow answer: %s holding %q", n.path, text)
	}
}

// TestCommittedThenOverwrittenIsAConflict: a save that landed, then failed a follow-up, and was
// overwritten by someone else before the read-back, is a conflict: the other writer's version is
// not adopted as the one the editor holds.
func TestCommittedThenOverwrittenIsAConflict(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb!": {"a.md", "one\n"}})
	sess := NewSession(d.sock, nil)
	wrote := false
	sess.beforeCall = func(method string, params []any) {
		if method == "doc.write" {
			wrote = true
		} else if method == "doc.read" && wrote {
			wrote = false
			d.write(t, "kb", "a.md", "someone else\n") // between the write and its read-back
		}
	}
	r := runTUI(t, sess, Options{})
	r.waitListed(t, 1)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	r.typeInEditor(t, "mine ")
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "note changed on disk")
	if !r.note().dirty {
		t.Error("the note reads as saved")
	}
}

// TestReconnects: when the daemon goes, the TUI says so and connects again to the next one on the
// socket, listing its notes.
func TestReconnects(t *testing.T) {
	d1 := startDaemonOn(t, "", map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d1.sock, nil), Options{})
	r.waitListed(t, 1)
	d1.stop()
	r.s.WaitFor(t, "the disconnect", func(sc string) bool {
		return strings.Contains(sc, "reconnecting") || strings.Contains(sc, "connecting to")
	})
	startDaemonOn(t, d1.sock, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r.s.WaitForText(t, "connected — autodoc")
	r.waitListed(t, 2)
}

// TestASaveNeverDropsANewerEdit: an edit made while a guarded save is on its way is not dropped by
// what the save guarded: the switch asks again.
func TestASaveNeverDropsANewerEdit(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	sess := NewSession(d.sock, nil)
	release := make(chan struct{})
	var holding atomic.Bool
	sess.beforeCall = func(method string, params []any) {
		if method == "doc.write" && holding.Load() {
			<-release
		}
	}
	r := runTUI(t, sess, Options{})
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	r.typeInEditor(t, "X")
	holding.Store(true)
	r.openByPicker(t, "b.md")
	r.s.WaitForText(t, "unsaved note")
	r.keys(t, key('s'))
	r.s.WaitForText(t, "saving a.md")
	r.typeInEditor(t, "Y") // while the save is held
	// the keys are the loop's to process: release the save only once the editor holds the edit and
	// is back in Normal mode (its own mode: the status line repaints after it)
	r.s.WaitFor(t, "the newer edit in the editor", func(string) bool {
		return strings.Contains(r.editorText(), "Y") && onLoop(r, func() bool { return r.h.editor.Mode() == widget.ModeNormal })
	})
	holding.Store(false)
	close(release)
	r.s.WaitForText(t, "changed again while it was saved")
	if n := r.note(); n.path != "a.md" || !n.dirty || !strings.Contains(r.editorText(), "Y") {
		t.Fatalf("after the save: %s, dirty %v, text %q", n.path, n.dirty, r.editorText())
	}
	r.keys(t, key('s')) // save the newer edit too; then the open goes ahead
	r.waitNote(t, "b.md")
	if got := d.read(t, "kb", "a.md"); !strings.Contains(got, "Y") || !strings.Contains(got, "X") {
		t.Errorf("a.md %q", got)
	}
}

// TestAFailedReloadKeepsTheEditsUnsaved: Reload from the conflict dialog marks the note clean only
// once the disk's version is in the editor; a read that fails leaves the edits guarded.
func TestAFailedReloadKeepsTheEditsUnsaved(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one\n", "b.md", "b\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	d.write(t, "kb", "a.md", "theirs\n")
	r.typeInEditor(t, "mine ")
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "note changed on disk")
	if err := d.fs["kb"].Remove(context.Background(), "a.md"); err != nil {
		t.Fatal(err)
	}
	r.keys(t, key('r'))
	r.s.WaitForText(t, "open a.md:")
	if n := r.note(); !n.dirty || !strings.Contains(r.editorText(), "mine") {
		t.Fatalf("after the failed reload: dirty %v, text %q", n.dirty, r.editorText())
	}
	r.openByPicker(t, "b.md")
	r.s.WaitForText(t, "unsaved note") // still guarded
}

func TestProgressText(t *testing.T) {
	for _, c := range []struct {
		docs, pending, emb int64
		want               string
	}{
		{0, 0, 0, ""},
		{3, 7, 0, "indexing ███░░░░░░░ 3/10"},
		{10, 0, 4, "embedding 4 pending"},
		{0, 5, 2, "indexing ░░░░░░░░░░ 0/5 · embedding 2 pending"},
	} {
		if got := progressText(c.docs, c.pending, c.emb); got != c.want {
			t.Errorf("%d %d %d: %q, want %q", c.docs, c.pending, c.emb, got, c.want)
		}
	}
}

// TestProgressWhileIndexing: while the daemon indexes, the status line shows a bar of the notes
// done; when it ends it says so once, and the notes, listed mid-scan, are listed again whole: the
// pickers' and the explorer's.
func TestProgressWhileIndexing(t *testing.T) {
	var notes []string
	for i := range 40 {
		notes = append(notes, fmt.Sprintf("n%02d.md", i), "note\n")
	}
	d := startDaemonWith(t, "", map[string][]string{"kb": notes}, daemonOpts{slow: 50 * time.Millisecond})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitFor(t, "the bar", func(sc string) bool { return strings.Contains(sc, "indexing ") && strings.Contains(sc, "/40") })
	// the explorer, opened on kb mid-scan: what is indexed so far
	r.leader(t, 'e')
	r.s.WaitForText(t, "explorer")
	r.keys(t, enter())
	under := func() int {
		return onLoop(r, func() int { return r.h.explorer.RowCount(&tuidecl.Index{Row: 0}) })
	}
	r.s.WaitFor(t, "kb listed", func(string) bool {
		return onLoop(r, func() bool { _, ok := r.h.explorerPaths["kb"]; return ok })
	})
	if n := under(); n >= 40 {
		t.Fatalf("the explorer had %d notes mid-scan: the cell needs a partial list", n)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(r.s.String(), "indexed 40 notes") {
		if time.Now().After(deadline) {
			t.Fatalf("indexing never ended on screen:\n%s", r.s.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.waitListed(t, 40)
	if sc := r.s.String(); strings.Contains(sc, "indexing ") {
		t.Errorf("the bar stayed:\n%s", sc)
	}
	r.s.WaitFor(t, "the explorer listed again, whole", func(string) bool { return under() == 40 })
}

// TestOnePollAfterSwitches: switching workspace (as a reconnect does) starts the next workspace's
// poll, and the old one's pending timer starts nothing: status is asked about once a second.
func TestOnePollAfterSwitches(t *testing.T) {
	d := startDaemon(t, map[string][]string{"alpha": {"a.md", "a\n"}, "beta": {"b.md", "b\n"}})
	sess := NewSession(d.sock, nil)
	var polls atomic.Int32
	sess.beforeCall = func(method string, params []any) {
		if method == "index.status" {
			polls.Add(1)
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· alpha")
	time.Sleep(1500 * time.Millisecond) // a poll's timer is pending now
	for _, ws := range []string{"beta", "alpha"} {
		r.h.p.Post(func() { r.h.enter(ws) })
		r.s.WaitForText(t, "· "+ws)
		time.Sleep(300 * time.Millisecond)
	}
	polls.Store(0)
	time.Sleep(3200 * time.Millisecond)
	// one loop polls 3 or 4 times in 3.2 s; each leaked one adds as many
	if n := polls.Load(); n > 4 {
		t.Errorf("%d status polls in 3.2 s after two switches: more than one poll loop", n)
	}
}
