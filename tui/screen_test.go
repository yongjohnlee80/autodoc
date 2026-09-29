package tui

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/highlight"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE WRITING SCREEN — the page alone until asked for more, centred at the ruler; the panels as
// drawers over it; the leader card; moving between the panes; the preferences, kept; the search's
// marks; the embedding provider form; a label over every field.

// runTUISized is runTUI on a w×h screen.
func runTUISized(t *testing.T, sess *Session, opt Options, w, h int) *running {
	t.Helper()
	host := newHost(sess, opt)
	s := decltest.RunWith(t, w, h, host.attach, host.options(opt)...)
	t.Cleanup(host.cancel)
	return &running{h: host, s: s}
}

// ready waits for the connection and the workspace's notes, which a hidden status line cannot say.
func (r *running) ready(t *testing.T) {
	t.Helper()
	r.s.WaitFor(t, "connected, the notes listed", func(string) bool {
		return onLoop(r, func() bool { return r.h.connected && len(r.h.notesAll) > 0 })
	})
}

// focused says whether the widget the document declares as id has the keyboard.
func (r *running) focused(id string) bool {
	return onLoop(r, func() bool {
		c, ok := r.h.p.Find(id)
		return ok && r.h.p.App().FocusWithin(c)
	})
}

func f10() tuicore.Event { return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF10} }

// TestTheScreenIsThePageAlone: by default, nothing but the page: no menu bar until F10 brings it
// up, no status line; Escape puts the menu bar away again.
func TestTheScreenIsThePageAlone(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	sc := r.s.String()
	for _, absent := range []string{"File", "NORMAL", "autodoc v-test", "explorer"} {
		if strings.Contains(sc, absent) {
			t.Fatalf("%q is on the blank screen:\n%s", absent, sc)
		}
	}
	if !strings.HasPrefix(sc, "┌ no note") {
		t.Fatalf("the page is not the screen's first row:\n%s", sc)
	}
	r.keys(t, f10())
	r.s.WaitFor(t, "the menu bar", func(sc string) bool { return strings.Contains(sc, "File") && strings.Contains(sc, "Help") })
	r.keys(t, esc(), esc())
	r.s.WaitFor(t, "the menu bar away", func(sc string) bool { return !strings.Contains(sc, "File") })
}

// TestTheStatusLineShowsWhileDisconnected: the status line hidden by preference still shows while
// the TUI is not connected, so a lost connection is never silent.
func TestTheStatusLineShowsWhileDisconnected(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	if sc := r.s.String(); strings.Contains(sc, "NORMAL") {
		t.Fatalf("the status line shows, connected:\n%s", sc)
	}
	d.stop()
	r.s.WaitFor(t, "the status line, disconnected", func(sc string) bool {
		return strings.Contains(sc, "NORMAL") && (strings.Contains(sc, "reconnecting") || strings.Contains(sc, "connecting to"))
	})
}

// col is the screen column (cells, not bytes) where sub starts in line, -1 when it is not there.
func col(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return len([]rune(line[:i]))
}

// TestThePageIsCentredAtTheRuler: the page is as wide as the ruler's columns and its border,
// centred; its right edge is the column past the ruler's; a line scrolled past it still shows the
// guide.
func TestThePageIsCentredAtTheRuler(t *testing.T) {
	long := strings.Repeat("x", 80)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "short\n" + long + "\n"}},
		daemonOpts{prefs: map[string]string{"tui.ruler": "60"}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 160, 20)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.s.WaitFor(t, "the note", func(string) bool { n := r.note(); return n.open && n.path == "a.md" })
	r.s.WaitForText(t, "short")
	rows := strings.Split(r.s.String(), "\n")
	// 62 wide on 160: 49 either side
	if l, rt := col(rows[0], "┌"), col(rows[0], "┐"); l != 49 || rt != 110 {
		t.Fatalf("the page spans columns %d to %d, want 49 to 110:\n%s", l, rt, r.s)
	}
	short := rows[1]
	if c := col(short, "short"); c != 50 {
		t.Fatalf("the text starts at column %d, want 50:\n%s", c, r.s)
	}
	// the page's right edge is the guide: no second line inside it
	if strings.Count(short, "│") != 2 {
		t.Fatalf("the short line's row has a guide inside the page as well as its edge: %q", short)
	}
	// the long line's end, in Normal mode: the view scrolls, and the guide shows inside the page
	r.keys(t, decltest.Alt('2'), key('j'), key('$'))
	r.s.WaitFor(t, "the guide inside the page", func(sc string) bool {
		row := strings.Split(sc, "\n")[1]
		return strings.Count(row, "│") == 3
	})
}

// TestThePanelsAreDrawersOverAStillPage: SPC e and SPC l open the explorer and the links over the
// page, from the edges their preferences name, and the page's text does not move; SPC e again
// closes it.
func TestThePanelsAreDrawersOverAStillPage(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "still text\n", "b.md", "see [[a]]\n"}},
		daemonOpts{prefs: map[string]string{"tui.status.shown": "true", "tui.explorer.edge": "right", "tui.links.edge": "bottom"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	where := func() (int, int) {
		x, y, ok := find(r.s.Backend.Snapshot(), "still text")
		if !ok {
			t.Fatalf("the page's text is not on screen:\n%s", r.s)
		}
		return x, y
	}
	x0, y0 := where()
	r.leader(t, 'e')
	r.s.WaitForText(t, "explorer")
	rows := strings.Split(r.s.String(), "\n")
	for _, row := range rows {
		if c := col(row, "┌ explorer"); c >= 0 && c < 50 {
			t.Fatalf("the explorer is not on the right:\n%s", r.s)
		}
	}
	if x, y := where(); x != x0 || y != y0 {
		t.Fatalf("the page's text moved from %d,%d to %d,%d under the explorer", x0, y0, x, y)
	}
	r.leader(t, 'e')
	r.s.WaitFor(t, "the explorer closed", func(sc string) bool { return !strings.Contains(sc, "explorer") })

	r.leader(t, 'l')
	r.s.WaitForText(t, "backlinks (1)")
	for i, row := range strings.Split(r.s.String(), "\n") {
		if strings.Contains(row, "backlinks (1)") && i < 15 {
			t.Fatalf("the links are not at the bottom (row %d):\n%s", i, r.s)
		}
	}
	if x, y := where(); x != x0 || y != y0 {
		t.Fatalf("the page's text moved from %d,%d to %d,%d under the links", x0, y0, x, y)
	}
	r.keys(t, esc()) // Escape in a panel closes it, and the page has the keyboard
	r.s.WaitFor(t, "the links closed", func(sc string) bool { return !strings.Contains(sc, "backlinks (") })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
}

// TestTheExplorerIsATreeOfEveryWorkspace: the explorer's top rows are the workspaces; a workspace
// opens to its folders then its notes, a folder to its own; Enter on a note opens it, entering
// its workspace when it is another, and closes the explorer.
func TestTheExplorerIsATreeOfEveryWorkspace(t *testing.T) {
	d := startDaemon(t, map[string][]string{
		"alpha": {"a.md", "# A\n", "dir/b.md", "# B\n"},
		"beta":  {"c.md", "# C\n"},
	})
	r := attached(t, d)
	r.s.WaitForText(t, "· alpha")
	r.leader(t, 'e')
	r.s.WaitFor(t, "the workspaces", func(sc string) bool { return strings.Contains(sc, "alpha") && strings.Contains(sc, "beta") })
	if !r.focused("explorerTree") {
		t.Fatal("the explorer opened without the keyboard")
	}
	r.keys(t, enter()) // alpha
	r.s.WaitFor(t, "alpha's folder, then its note", func(sc string) bool {
		f, n := strings.Index(sc, "dir/"), strings.Index(sc, "a.md")
		return f >= 0 && n > f
	})
	r.keys(t, key('j'), enter()) // dir/
	r.s.WaitForText(t, "b.md")
	r.keys(t, key('j'), enter()) // dir/b.md
	r.waitNote(t, "dir/b.md")
	r.s.WaitFor(t, "the explorer closed", func(sc string) bool { return !strings.Contains(sc, "┌ explorer") })

	// another workspace's note: its workspace is entered
	r.leader(t, 'e')
	r.s.WaitForText(t, "beta")
	r.keys(t, key('G'), enter()) // beta, the last row
	r.s.WaitForText(t, "c.md")
	r.keys(t, key('j'), enter())
	r.s.WaitForText(t, "· beta")
	r.waitNote(t, "c.md")
}

// TestPanesMoveInNormalModeOnly: Ctrl+h/j/k/l move between the page and the panel open at that
// edge, in Normal mode; in Insert mode they are the editor's, and the keyboard stays.
func TestPanesMoveInNormalModeOnly(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
	r.leader(t, 'e') // on the left, with the keyboard
	r.s.WaitForText(t, "explorer")
	moved := func(what, id string) {
		t.Helper()
		r.s.WaitFor(t, what, func(string) bool { return r.focused(id) })
	}
	moved("the explorer has the keyboard", "explorerTree")
	r.keys(t, decltest.Ctrl('l'))
	moved("back to the page", "editor")
	r.keys(t, decltest.Ctrl('j')) // no panel below: nothing moves
	r.keys(t, decltest.Ctrl('h'))
	moved("to the explorer", "explorerTree")
	r.keys(t, decltest.Ctrl('l'))
	moved("back to the page again", "editor")

	r.keys(t, key('i'))
	r.s.WaitFor(t, "Insert mode", func(string) bool {
		return onLoop(r, func() bool { return r.h.editor.Mode().String() == "INSERT" })
	})
	r.keys(t, decltest.Ctrl('h'))
	r.keys(t, decltest.Type("z")...) // a key after it, so the Ctrl+H has been handled
	r.s.WaitFor(t, "the z typed", func(string) bool { return strings.Contains(r.editorText(), "z") })
	if !r.focused("editor") {
		t.Fatal("Ctrl+H in Insert mode moved the keyboard out of the editor")
	}
}

// TestPreferencesAreKept: a preference changed is in the daemon's store at once, and a TUI
// started later reads it; the page's width takes a number of columns in its range.
func TestPreferencesAreKept(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.leader(t, 't') // the status line, shown by the tests' preferences: hidden
	r.s.WaitFor(t, "the status line hidden", func(sc string) bool { return !strings.Contains(sc, "NORMAL") })
	pref := func(name string) string {
		t.Helper()
		var v string
		r.s.WaitFor(t, name+" stored", func(string) bool {
			m, err := d.db.Preferences(context.Background())
			v = m[name]
			return err == nil && v != ""
		})
		return v
	}
	if v := pref("tui.status.shown"); v != "false" {
		t.Fatalf("tui.status.shown = %q", v)
	}

	r.leader(t, ',')
	r.s.WaitForText(t, "preferences")
	r.h.p.Post(func() { r.h.setRuler("30") })
	r.s.WaitForText(t, "40 to 400")
	r.h.p.Post(func() { r.h.setRuler("80") })
	r.s.WaitFor(t, "the width stored", func(string) bool {
		m, _ := d.db.Preferences(context.Background())
		return m["tui.ruler"] == "80"
	})
	if w := onLoop(r, func() int { return r.h.prefs.ruler }); w != 80 {
		t.Fatalf("the page is %d columns after 80", w)
	}

	// a TUI started later: the status line hidden, the page 80 columns
	r2 := runTUI(t, NewSession(d.sock, nil), Options{})
	r2.ready(t)
	r2.s.WaitFor(t, "the kept preferences", func(string) bool {
		return onLoop(r2, func() bool { return !r2.h.prefs.statusOn && r2.h.prefs.ruler == 80 })
	})
}

// TestTheSearchMarksItsWordsWhereTheyAre: the preview marks each of the search's words, case
// aside, over exactly its bytes, however lower-casing changes the line's length before it.
func TestTheSearchMarksItsWordsWhereTheyAre(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	hl := h.searchHighlighter()
	marked := func(line string) []string {
		spans, _ := hl.HighlightBlock(line, 0)
		var out []string
		for _, s := range spans {
			if s.Style == highlight.Alert {
				out = append(out, line[s.Start:s.End])
			}
		}
		return out
	}
	h.marks.Store(nil)
	if got := marked("a kestrel"); len(got) != 0 {
		t.Fatalf("no search, yet marked %q", got)
	}
	h.marks.Store(termsOf(`Kestrel "plov*"`))
	for line, want := range map[string]string{
		"a kestrel and a PLOVER":   "kestrel,PLOV",
		"İstanbul's kestrel":       "kestrel", // İ lower-cases to one byte fewer
		"ẞ KESTREL":                "KESTREL", // and ẞ to one more
		"no birds here":            "",
		"kestrelkestrel is marked": "kestrelkestrel", // two marks side by side, one run
	} {
		if got := strings.Join(marked(line), ","); got != want {
			t.Errorf("%q: marked %q, want %q", line, got, want)
		}
	}
}

// TestTheProviderForm: Preferences › Add… offers the kinds; a local Ollama takes no key, Ollama
// Cloud one (required); List models asks the provider, with the key typed, and Enter on one
// writes it; Save keeps the provider, its key sealed and never on screen.
func TestTheProviderForm(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "nomic-embed-text"}, {"name": "gpt-oss:20b"}}})
	}))
	defer ollama.Close()
	d := startManaged(t, map[string]string{"kb": noteDir(t, "a.md", "a\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.leader(t, ',')
	r.s.WaitForText(t, "embedding providers")
	r.keys(t, key('a'))
	r.s.WaitForText(t, "add an embedding provider")
	if sc := r.s.String(); strings.Contains(sc, "API key") || !strings.Contains(sc, "Ollama (local)") {
		t.Fatalf("a local Ollama's form:\n%s", sc)
	}
	r.keys(t, decltest.Type("cloud")...)
	r.keys(t, tab(), enter()) // the kind: its choices
	r.s.WaitForText(t, "Ollama Cloud")
	r.keys(t, key('j'), enter())
	r.s.WaitFor(t, "the key field, and the kind's URL", func(sc string) bool {
		return strings.Contains(sc, "API key (required)") && strings.Contains(sc, "ollama.com")
	})
	r.keys(t, tab(), decltest.Ctrl('u'))
	r.keys(t, decltest.Type(ollama.URL)...)
	r.keys(t, tab(), tab()) // past the model, to the key
	r.keys(t, decltest.Type("sekrit")...)
	r.s.WaitForText(t, "••••••")
	// a button's letter works where no field takes it: in the models' list
	r.keys(t, tab(), key('l')) // List models
	r.s.WaitForText(t, "2 models")
	mu.Lock()
	if len(auth) != 1 || auth[0] != "Bearer sekrit" {
		t.Errorf("the provider was asked with %q", auth)
	}
	mu.Unlock()
	r.keys(t, key('j'), enter()) // the models by name: the second, into the model field
	r.s.WaitFor(t, "the model written", func(sc string) bool {
		return strings.Contains(sc, "nomic-embed-text") && !strings.Contains(sc, "nomic-embed-text, say")
	})
	r.keys(t, key('s'))
	r.s.WaitForText(t, "saved the provider cloud")
	if strings.Contains(r.s.String(), "sekrit") {
		t.Fatalf("the key is on screen:\n%s", r.s)
	}
	info, sealed, err := d.db.ProviderWithKey(context.Background(), "cloud")
	if err != nil || info.Kind != "ollama-cloud" || info.BaseURL != ollama.URL || info.Model != "nomic-embed-text" || sealed != "sekrit" {
		t.Fatalf("stored %+v, key kept %v, %v", info, sealed == "sekrit", err)
	}

	// edited, its key sealed: another kind that takes a key still keeps it when left empty
	r.s.WaitForText(t, "cloud")
	r.h.p.Post(func() { r.h.startEditProvider(0) })
	r.s.WaitForText(t, "API key (sealed; leave empty to keep it)")
	r.keys(t, tab(), enter()) // the kind
	r.s.WaitForText(t, "OpenAI-compatible")
	r.keys(t, key('j'), enter())
	r.s.WaitFor(t, "the kind changed, the key still sealed", func(sc string) bool {
		return strings.Contains(sc, "OpenAI-compatible") && strings.Contains(sc, "API key (sealed; leave empty to keep it)")
	})
}

// TestEveryFieldHasALabel: every TextField the QML declares has a Text over it, saying what goes
// in it (the folder dialog's path field is its own, and labelled).
func TestEveryFieldHasALabel(t *testing.T) {
	n := 0
	err := fs.WalkDir(qmlEmbed, "qml", func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".qml") {
			return err
		}
		src, err := fs.ReadFile(qmlEmbed, p)
		if err != nil {
			return err
		}
		tree, err := qml.QML{File: p}.Parse(src)
		if err != nil {
			return err
		}
		var walk func(*qml.SpecNode)
		walk = func(node *qml.SpecNode) {
			for i, c := range node.Children {
				if c.Type == "TextField" {
					n++
					if i == 0 || node.Children[i-1].Type != "Text" {
						t.Errorf("%s: the TextField at %s has no Text over it", p, c.Pos)
					}
				}
				walk(c)
			}
		}
		walk(tree.Root)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 8 {
		t.Fatalf("only %d TextFields found: the walk missed the dialogs", n)
	}
}

// fakeOllama answers as an Ollama server with the models named: its tags, and three-number
// embeddings. down makes every embed call fail, as a server at its usage limit.
type fakeOllama struct {
	*httptest.Server
	mu   sync.Mutex
	down bool
}

func newFakeOllama(t *testing.T, models ...string) *fakeOllama {
	f := &fakeOllama{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			var list []map[string]string
			for _, m := range models {
				list = append(list, map[string]string{"name": m, "model": m, "digest": "sha256:" + m})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": list})
		case "/api/embed":
			f.mu.Lock()
			down := f.down
			f.mu.Unlock()
			if down {
				http.Error(w, "usage limit", http.StatusTooManyRequests)
				return
			}
			var req struct{ Input []string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			vecs := make([][]float32, len(req.Input))
			for i := range vecs {
				vecs[i] = []float32{1, 0, 0}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs, "prompt_eval_count": 2 * len(req.Input)})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// TestAProviderInUse: Use sets a provider up and semantic search runs with it, its usage and
// calls shown under the list; one that does not set up is refused, the one in use staying; Words
// only turns it off; Remove asks, then removes it.
func TestAProviderInUse(t *testing.T) {
	ollama := newFakeOllama(t, "embedder")
	root := noteDir(t, "a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n")
	d := startManaged(t, map[string]string{"kb": root})
	ctx := context.Background()
	for _, sp := range []store.ProviderSpec{
		{Name: "local", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "embedder"},
		{Name: "missing", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "no-such-model"},
	} {
		if _, err := d.db.AddProvider(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.leader(t, ',')
	r.s.WaitForText(t, "semantic search off")
	r.keys(t, key('u')) // the first row: local
	r.s.WaitForText(t, "semantic search with local")
	// its calls, metered and written every couple of seconds, under the list
	r.s.WaitFor(t, "local's usage", func(sc string) bool {
		r.h.p.Post(func() { r.h.providerDetail(0) })
		return strings.Contains(sc, "requests ·") && strings.Contains(sc, "latest calls") && strings.Contains(sc, "· ok")
	})
	// at its usage limit: a new note's embedding is refused, and the log says why
	ollama.mu.Lock()
	ollama.down = true
	ollama.mu.Unlock()
	if err := os.WriteFile(filepath.Join(root, "c.md"), []byte("# C\n\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the refused call", func(sc string) bool {
		r.h.p.Post(func() { r.h.providerDetail(0) })
		return strings.Contains(sc, "at the usage limit") && strings.Contains(sc, "rate limited")
	})

	// a provider that does not set up: refused, and local stays in use
	r.h.p.Post(func() { r.h.useProvider(1) })
	r.s.WaitFor(t, "missing refused", func(sc string) bool {
		return strings.Contains(sc, "missing not used") && strings.Contains(sc, "no-such-model")
	})
	if got := onLoop(r, func() string { return r.h.activeProvider }); got != "local" {
		t.Fatalf("after a refused Use the provider in use is %q", got)
	}

	r.keys(t, key('w')) // Words only
	r.s.WaitForText(t, "semantic search off")
	r.keys(t, key('r')) // Remove…, the first row
	r.s.WaitForText(t, "remove the provider?")
	r.s.WaitForText(t, "Remove the provider local?")
	r.keys(t, key('y'))
	// the list, not the status line: a scan ending ("indexed 3 notes") can replace the message
	r.s.WaitFor(t, "local gone from the list", func(sc string) bool {
		return strings.Contains(sc, "no-such-model") && !strings.Contains(sc, "local ")
	})
	if ps, err := d.db.Providers(ctx); err != nil || len(ps) != 1 || ps[0].Name != "missing" {
		t.Fatalf("after the remove: %+v, %v", ps, err)
	}
}

// TestEveryCommandRefusesArgumentsNotItsOwn: a command the document calls with the wrong arguments
// (a QML edit that got a call wrong) is refused, saying what it takes, and runs nothing.
func TestEveryCommandRefusesArgumentsNotItsOwn(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	b := qml.SpecValue{Kind: qml.SpecValueBool, Raw: "true"}
	frac := qml.SpecValue{Kind: qml.SpecValueNumber, Raw: "1.5"}
	s := qml.SpecValue{Kind: qml.SpecValueString, Raw: "x"}
	// each list is one no command takes: a Host with no program would panic running any
	bad := [][]qml.SpecValue{{b}, {b, b}, {b, b, b, b}, {frac}, {frac, s}, {s, s, s, b}}
	for name, fn := range h.commands() {
		for _, args := range bad {
			if err := fn(args); err == nil {
				t.Errorf("%s accepted %v", name, args)
			}
		}
	}
}

// TestTheFolderPickerStartsAtHome: adding a workspace starts in the home directory, or at the root
// when there is none.
func TestTheFolderPickerStartsAtHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := homeDir(); got != home {
		t.Errorf("with a home: %q", got)
	}
	t.Setenv("HOME", "")
	if got := homeDir(); got != "/" {
		t.Errorf("with none: %q, want /", got)
	}
}

// TestAListingThatFailsSaysSo: the workspace's notes not listed (a workspace gone from the daemon)
// is on the status line, and the pickers list nothing.
func TestAListingThatFailsSaysSo(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.h.p.Post(func() { r.h.enter("gone") })
	r.s.WaitForText(t, "notes: ")
	if got := r.listed(); len(got) != 0 {
		t.Errorf("the pickers list %v for a workspace that failed to list", got)
	}
}

// TestAHitOpensWhereItIsAfterJoinedCharacters: the cursor opens on the hit's first character,
// the preview's too, however many runes the characters before it are made of (a combining accent,
// a family emoji joined by ZWJs, a CRLF line break).
func TestAHitOpensWhereItIsAfterJoinedCharacters(t *testing.T) {
	joined := "# A\r\n\r\ncafé \U0001F468‍\U0001F469‍\U0001F467 x\r\n\r\n## Birds\r\n\r\nkestrel notes\r\n"
	if got, want := cursorAt(joined, strings.Index(joined, "## Birds")), 3+1+1+8+1+1; got != want {
		t.Errorf("cursorAt before ## Birds = %d, want %d (3, a break, a break, 8 characters, a break, a break)", got, want)
	}
	// a byte inside a character is that character: the one after e starts the accent, which is é's
	if got := cursorAt("xe\u0301y", strings.Index("xe\u0301y", "\u0301")); got != 1 {
		t.Errorf("cursorAt inside é = %d, want 1 (é's start)", got)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", joined}})
	r := attached(t, d)
	r.keys(t, decltest.Ctrl('g'))
	r.s.WaitForText(t, "words; a * ends a prefix")
	r.keys(t, decltest.Type("kestrel")...)
	r.s.WaitForText(t, "hits (1)")
	r.keys(t, enter())
	r.waitNote(t, "a.md")
	at := onLoop(r, func() [2]int { l, c := r.h.editor.Line(); return [2]int{l, c} })
	line := onLoop(r, func() string { return r.h.editor.Lines()[at[0]] })
	if !strings.HasPrefix(line, "kestrel") && !strings.HasPrefix(line, "## Birds") || at[1] != 0 {
		t.Errorf("the cursor opened at line %d col %d (%q), not at the hit's start", at[0], at[1], line)
	}
}
