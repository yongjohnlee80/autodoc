package tui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/store"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// startManaged runs the daemon's own workspaces (internal/daemon) over a store holding roots
// (name → a directory on disk), and its embedding providers, as --serve does, so the workspace
// and embedding verbs work.
func startManaged(t *testing.T, roots map[string]string) *managedDaemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range testPrefs {
		if err := db.SetPreference(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range sortedNames(roots) {
		if _, err := db.AddWorkspace(ctx, name, roots[name], nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	ws := serving.New(ctx, db, serving.Options{Poll: 20 * time.Millisecond, BatchDelay: 5 * time.Millisecond})
	if err := ws.OpenAll(); err != nil {
		t.Fatal(err)
	}
	// wait until every root's notes are indexed, so the first listing has them
	for _, w := range ws.List() {
		want, _ := filepath.Glob(filepath.Join(w.Root, "*.md"))
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			st, _ := w.Index.Status(ctx)
			if st.Docs == int64(len(want)) && st.PendingJobs == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("workspace %s did not index", w.Name)
			}
		}
	}
	dir, err := os.MkdirTemp("", "adm")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	emb := serving.NewEmbedding(db, ws, nil)
	emb.Start(ctx)
	srv := rpc.New(ws, "v-test", rpc.WithListener(ln), rpc.WithPreferences(db), rpc.WithEmbeddings(emb))
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		emb.Wait()
		ws.StopAll()
		_ = db.Close()
		_ = os.RemoveAll(dir)
	})
	return &managedDaemon{sock: sock, db: db}
}

// managedDaemon is a managed daemon's handle: its socket, and its store.
type managedDaemon struct {
	sock string
	db   *store.Store
}

func sortedNames(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func noteDir(t *testing.T, notes ...string) string {
	t.Helper()
	d := t.TempDir()
	for i := 0; i < len(notes); i += 2 {
		if err := os.WriteFile(filepath.Join(d, notes[i]), []byte(notes[i+1]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func tab() tuicore.Event { return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab} }

// addWorkspace fills the add dialog: the title over "untitled", then the folder typed into the
// path, and Enter selects it.
func (r *running) addWorkspace(t *testing.T, title, root string) {
	t.Helper()
	r.keys(t, key('a'))
	r.s.WaitForText(t, "add a workspace")
	r.s.WaitForText(t, "untitled") // the title field, which has the keyboard first
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type(title)...)
	r.keys(t, decltest.Ctrl('j'), decltest.Ctrl('u')) // the path
	r.keys(t, decltest.Type(root)...)
	r.keys(t, enter())
}

// TestWorkspaceManager: from the picker, the manager adds a workspace (a refused add says why),
// renames it, and deletes it after asking what goes and what stays; the files stay.
func TestWorkspaceManager(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": noteDir(t, "a.md", "alpha\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.waitListed(t, 1)

	r.keys(t, decltest.Ctrl('w'))
	r.s.WaitForText(t, "Manage…")
	r.keys(t, key('m'))
	r.s.WaitForText(t, "Rename…")

	root := noteDir(t, "b.md", "beta\n")
	r.addWorkspace(t, "notes", root)
	r.s.WaitForText(t, "added workspace notes")
	r.s.WaitFor(t, "the manager lists it", func(sc string) bool { return strings.Contains(sc, root) })

	// a refusal opens the dialog again, the reason on the status line
	r.addWorkspace(t, "notes", t.TempDir())
	r.s.WaitFor(t, "the refusal, the dialog open again", func(sc string) bool {
		return strings.Contains(sc, "not added: another workspace has this name or root") && strings.Contains(sc, "add a workspace")
	})
	r.keys(t, esc())
	r.s.WaitFor(t, "the add closed", func(sc string) bool { return !strings.Contains(sc, "add a workspace") })

	// the rows are by name: kb, then notes
	r.keys(t, key('j'), key('r'))
	r.s.WaitForText(t, "rename the workspace")
	r.keys(t, decltest.Type("-2")...)
	r.keys(t, enter())
	r.s.WaitForText(t, "renamed notes to notes-2")

	r.keys(t, key('d'))
	r.s.WaitForText(t, "delete the workspace?")
	// the question wraps where the root's length puts it: read it as running text
	r.s.WaitFor(t, "the files stay", func(sc string) bool {
		return strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " "), "stay as they are")
	})
	r.keys(t, key('y'))
	r.s.WaitForText(t, "deleted workspace notes-2 (its files stay)")
	r.s.WaitFor(t, "the manager no longer lists it", func(sc string) bool { return !strings.Contains(sc, root) })
	if _, err := os.Stat(filepath.Join(root, "b.md")); err != nil {
		t.Errorf("deleting the workspace touched its files: %v", err)
	}
	if got := onLoop(r, func() string { return r.h.ws }); got != "kb" {
		t.Errorf("the workspace in use is %q after deleting another, want kb", got)
	}
	// the one in use, and the last: the TUI is left with none, and says how to add one
	r.keys(t, key('k'), key('d'))
	r.s.WaitForText(t, "Delete the workspace kb?")
	r.keys(t, key('y'))
	r.s.WaitForText(t, "no workspace: Go › Manage workspaces… adds one")
	if got := onLoop(r, func() string { return r.h.ws }); got != "" {
		t.Errorf("the workspace in use is %q after deleting it, want none", got)
	}
}

func TestWorkspaceSectionSizeIsEditedAtTheWorkspace(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": noteDir(t, "n.md", "# Notes\n\n"+strings.Repeat("many words here in a paragraph.\n\n", 70))})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.manageWorkspaces() })
	r.s.WaitForText(t, "SECTION")
	r.h.p.Post(func() { r.h.startSectionSize(0) })
	r.s.WaitForText(t, "section size · kb")
	r.h.p.Post(func() { r.h.saveSectionSize("not a number") })
	r.s.WaitForText(t, "section size must be 128–2048")
	r.h.p.Post(func() { r.h.saveSectionSize("127") })
	r.s.WaitForText(t, "section size must be 128–2048")
	r.h.p.Post(func() { r.h.saveSectionSize("256") })
	r.s.WaitForText(t, "section size 256; re-chunking")
	ws, err := d.db.Workspaces(context.Background())
	if err != nil || len(ws) != 1 {
		t.Fatalf("workspace listing: %v, %v", ws, err)
	}
	if got, err := d.db.SectionTokens(context.Background(), ws[0].ID); err != nil || got != 256 {
		t.Fatalf("section size = %d, %v; want 256", got, err)
	}
}

// TestOpensTheNamedWorkspace: Options.Workspace (autodoc --ui <name>) opens that one, not the
// first, and Remember is told each workspace the TUI enters.
func TestOpensTheNamedWorkspace(t *testing.T) {
	d := startManaged(t, map[string]string{"alpha": noteDir(t, "a.md", "a\n"), "beta": noteDir(t, "b.md", "b\n", "c.md", "c\n")})
	var mu sync.Mutex
	var entered []string
	r := runTUI(t, NewSession(d.sock, nil), Options{Workspace: "beta", Remember: func(n string) {
		mu.Lock()
		entered = append(entered, n)
		mu.Unlock()
	}})
	r.s.WaitForText(t, "· beta")
	r.waitListed(t, 2)
	mu.Lock()
	defer mu.Unlock()
	if len(entered) != 1 || entered[0] != "beta" {
		t.Errorf("Remember was told %v, want [beta]", entered)
	}
}
