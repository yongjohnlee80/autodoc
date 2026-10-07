package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yongjohnlee80/autodoc/rpc"
	"github.com/yongjohnlee80/golib/dao/sqlite"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// ready waits for the connection and the workspace's files, which a hidden status line cannot say.
func (r *running) ready(t *testing.T) {
	t.Helper()
	r.s.WaitFor(t, "connected, the notes listed", func(string) bool {
		return onLoop(r, func() bool { return r.h.connected && len(r.h.filesAll) > 0 })
	})
}

// focused says whether the widget the document declares as id has the keyboard.
// searchReady waits for the search dialog with its query field focused: a key typed before the
// focus lands there reaches the page instead (a race a loaded CI runner loses). The field's id is
// its component's, and an open dialog's card is in the overlay, so the test reads what the user
// sees: the caret on the field's row, the one under "search: words".
func (r *running) searchReady(t *testing.T) {
	t.Helper()
	r.s.WaitFor(t, "the caret in the search field", func(sc string) bool {
		row := -1
		for i, line := range strings.Split(sc, "\n") {
			if strings.Contains(line, "search: words") {
				row = i
				break
			}
		}
		_, y, shown := r.s.Backend.CursorPos()
		return row >= 0 && shown && y == row+1
	})
}

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
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{"tui.toast.seconds": "1"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	// the notifications of the start (connected, indexed) go once they have lingered: gone from
	// the model AND from the painted screen, which repaints a frame after the model empties
	r.s.WaitFor(t, "the start's toasts gone", func(sc string) bool {
		return !strings.Contains(sc, "╭") && onLoop(r, func() bool { s, w := r.h.toasts.Len(); return s == 0 && w == 0 })
	})
	sc := r.s.String()
	for _, absent := range []string{"File", "NORMAL", "autodoc v-test", "explorer"} {
		if strings.Contains(sc, absent) {
			t.Fatalf("%q is on the blank screen:\n%s", absent, sc)
		}
	}
	if !strings.HasPrefix(sc, "┌ untitled") {
		t.Fatalf("the page is not the screen's first row:\n%s", sc)
	}
	r.keys(t, f10())
	r.s.WaitFor(t, "the menu bar", func(sc string) bool { return strings.Contains(sc, "File") && strings.Contains(sc, "Help") })
	r.keys(t, esc(), esc())
	r.s.WaitFor(t, "the menu bar away", func(sc string) bool { return !strings.Contains(sc, "File") })
}

func TestLeaderMenuToggleFocusesOnlyWhenShown(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.leader(t, 'm')
	r.s.WaitFor(t, "the shown bar focused", func(sc string) bool {
		return strings.Contains(strings.Split(sc, "\n")[0], "File") && r.focused("menuBar")
	})
	r.keys(t, enter())
	r.s.WaitForText(t, "New file")
	r.keys(t, decltest.Ctrl(' '))
	r.s.WaitForText(t, "SPC — commands")
	r.keys(t, key('m'))
	r.s.WaitFor(t, "the bar hidden and page focused", func(sc string) bool {
		return !strings.Contains(strings.Split(sc, "\n")[0], "File") && r.focused("editor") && !r.focused("menuBar")
	})
}

// TestAMenuTitleIsClickedUnderTheToasts: a click on a menu bar title opens its menu while the
// toasts are up. The toasts are an always-shown float over the whole screen, and its empty layer
// took every click (Johno: "the autodoc mouse button on the menu not working anymore"); golib's
// hit-testing now lets the pointer through a non-modal float wherever its content is not.
func TestAMenuTitleIsClickedUnderTheToasts(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}},
		daemonOpts{prefs: map[string]string{"tui.menu.autohide": "false", "tui.status.shown": "true"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "connected — autodoc v-test") // a toast is up
	r.s.WaitFor(t, "the menu bar", func(sc string) bool { return strings.Contains(strings.Split(sc, "\n")[0], "File") })
	row := strings.Split(r.s.String(), "\n")[0]
	x := len([]rune(row[:strings.Index(row, "File")])) + 1
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: 0},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: 0})
	r.s.WaitForText(t, "New file")
}

// TestAToastClickedOpensTheNotificationsHistory: a click on a toast's card opens the history of the
// notifications, where it and the ones before it are kept.
func TestAToastClickedOpensTheNotificationsHistory(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{"tui.toast.seconds": "10"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "connected — autodoc v-test")
	var x, y = -1, -1
	for i, row := range strings.Split(r.s.String(), "\n") {
		if j := strings.Index(row, "connected — autodoc v-test"); j >= 0 {
			x, y = len([]rune(row[:j])), i
		}
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.s.WaitFor(t, "the history open", func(string) bool { return onLoop(r, func() bool { return r.h.historyOpen }) })
	r.s.WaitForText(t, "TIME")
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
		daemonOpts{prefs: map[string]string{"tui.ruler": "60", "tui.editor.wrap": "false"}}) // scrolled, not wrapped
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 160, 20)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.s.WaitFor(t, "the note", func(string) bool { n := r.file(); return n.open && n.path == "a.md" })
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
// closes it. A side panel takes the middle 85% of the rows, one at the top or bottom the middle
// 80% of the columns: the page shows around it.
func TestThePanelsAreDrawersOverAStillPage(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "still text\n", "b.md", "see [[a]]\n"}},
		daemonOpts{prefs: map[string]string{"tui.status.shown": "true", "tui.explorer.edge": "right", "tui.links.edge": "bottom"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
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
	top := -1
	for i, row := range rows {
		if c := col(row, "┌ explorer"); c >= 0 {
			if c < 50 {
				t.Fatalf("the explorer is not on the right:\n%s", r.s)
			}
			top = i
		}
	}
	// 85% of the rows, centred: rows free above it and below it
	if want := (len(rows) - len(rows)*85/100) / 2; top < want-1 || top > want+1 || top < 1 {
		t.Fatalf("the explorer starts at row %d of %d, want about %d:\n%s", top, len(rows), want, r.s)
	}
	if x, y := where(); x != x0 || y != y0 {
		t.Fatalf("the page's text moved from %d,%d to %d,%d under the explorer", x0, y0, x, y)
	}
	r.leader(t, 'e')
	r.s.WaitFor(t, "the explorer closed", func(sc string) bool { return !strings.Contains(sc, "explorer") })

	r.leader(t, 'l')
	r.s.WaitForText(t, "backlinks (1)")
	for i, row := range strings.Split(r.s.String(), "\n") {
		if !strings.Contains(row, "backlinks (1)") {
			continue
		}
		if i < 15 {
			t.Fatalf("the links are not at the bottom (row %d):\n%s", i, r.s)
		}
		// 80% of the columns, centred: its border a tenth in
		w := len([]rune(row))
		if c := col(row, "┌"); c < w/10-1 || c > w/10+1 {
			t.Fatalf("the links start at column %d of %d, want about %d:\n%s", c, w, w/10, r.s)
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
// opens to its folders then its files, a folder to its own; Enter on a file opens it, entering
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
	r.waitFile(t, "dir/b.md")
	r.s.WaitFor(t, "the explorer closed", func(sc string) bool { return !strings.Contains(sc, "┌ explorer") })

	// another workspace's note: its workspace is entered
	r.leader(t, 'e')
	r.s.WaitForText(t, "beta")
	r.keys(t, key('G'), enter()) // beta, the last row
	r.s.WaitForText(t, "c.md")
	r.keys(t, key('j'), enter())
	r.s.WaitForText(t, "· beta")
	r.waitFile(t, "c.md")
}

// TestPanesMoveInNormalModeOnly: Ctrl+h/j/k/l move between the page and the panel open at that
// edge, in Normal mode; in Insert mode they are the editor's, and the keyboard stays.
func TestPanesMoveInNormalModeOnly(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
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
	// the store holds the tests' "true" before the toggle's write lands: wait for the value
	// written, not any value
	r.s.WaitFor(t, "tui.status.shown = false stored", func(string) bool {
		m, err := d.db.Preferences(context.Background())
		return err == nil && m["tui.status.shown"] == "false"
	})

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

// TestPreferencesAreStoredInTheOrderTheyChanged: a preference changed twice in a row is stored as
// the second change, however long the first one's write takes to reach the daemon.
func TestPreferencesAreStoredInTheOrderTheyChanged(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	var holding atomic.Bool
	var answered atomic.Int32
	overtaken := make(chan struct{}) // a write answered while the first was held
	once := sync.OnceFunc(func() { close(overtaken) })
	sess := NewSession(d.sock, nil)
	sess.beforeCall = func(method string, p []any) {
		if method == "preference.set" && p[1] == "top" {
			// held until a later write overtakes it, or a while: written one at a time, the
			// next is not sent before this one is answered, and nothing overtakes it
			holding.Store(true)
			select {
			case <-overtaken:
			case <-time.After(300 * time.Millisecond):
			}
			holding.Store(false)
		}
	}
	sess.afterCall = func(method string) {
		if method == "preference.set" {
			if holding.Load() {
				once()
			}
			answered.Add(1)
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "connected — autodoc v-test")
	onLoop(r, func() bool {
		r.h.setTerminalEdge(indexOf(termEdges, "top"))
		r.h.setTerminalEdge(indexOf(termEdges, "right"))
		return true
	})
	r.s.WaitFor(t, "both writes answered", func(string) bool { return answered.Load() == 2 })
	m, err := d.db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if e := m["tui.terminal.edge"]; e != "right" {
		t.Fatalf("stored edge %q after top then right: the earlier write landed last", e)
	}
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
// writes it; the context window starts at the default and takes a number; Save keeps the
// provider, its key sealed and never on screen.
func TestTheProviderForm(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			_ = json.NewEncoder(w).Encode(map[string]any{"model_info": map[string]any{"nomic.context_length": 4096}})
			return
		}
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
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "a\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.leader(t, 'a') // System › AI models
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
	r.keys(t, tab(), tab()) // past the model, to the context window: the default, to begin with
	r.s.WaitForText(t, "context window")
	if !strings.Contains(r.s.String(), "8192") {
		t.Fatalf("the context window does not start at the default:\n%s", r.s)
	}
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type("4096")...)
	r.keys(t, tab()) // to the key
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
	r.s.WaitForText(t, "model max 4096")
	r.h.p.Post(func() { r.h.saveProvider("cloud", ollama.URL, "nomic-embed-text", "", "8192") })
	r.s.WaitForText(t, "exceeds this model's maximum of 4096")
	r.keys(t, key('s'))
	r.s.WaitForText(t, "saved the provider cloud")
	if strings.Contains(r.s.String(), "sekrit") {
		t.Fatalf("the key is on screen:\n%s", r.s)
	}
	info, sealed, err := d.db.ProviderWithKey(context.Background(), "cloud")
	if err != nil || info.Kind != "ollama-cloud" || info.BaseURL != ollama.URL || info.Model != "nomic-embed-text" || info.Context != 4096 || sealed != "sekrit" {
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
	// back to Ollama Cloud, and List models with no key typed: the stored key is the one used
	r.keys(t, enter(), key('k'), enter())
	r.s.WaitFor(t, "Ollama Cloud again", func(sc string) bool {
		return strings.Contains(sc, "Ollama Cloud") && !strings.Contains(sc, "OpenAI-compatible")
	})
	r.h.p.Post(func() { r.h.listModels(ollama.URL, "") })
	r.s.WaitForText(t, "2 models")
	mu.Lock()
	last := auth[len(auth)-1]
	mu.Unlock()
	if last != "Bearer sekrit" {
		t.Errorf("an edited provider's models were asked for with %q, not its stored key", last)
	}
	// a context window that is not a number is refused in the form, the store unchanged
	r.h.p.Post(func() { r.h.saveProvider("cloud", ollama.URL, "nomic-embed-text", "", "lots") })
	r.s.WaitForText(t, "the context window is a number of tokens")
	if info, _, err := d.db.ProviderWithKey(context.Background(), "cloud"); err != nil || info.Context != 4096 {
		t.Errorf("after the refused window: %+v, %v", info, err)
	}
}

// A provider coming into use is a new embedding model: the picker's query and hits answered by
// words alone are cleared, the field emptied, and the clearing said (Johno, 2026-10-02: "When the
// embedding model changes, it should clear the search query, and start afresh").
func TestAModelChangeClearsTheSearch(t *testing.T) {
	o := newFakeOllama(t, "embedder")
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# Wildlife\n\nzebra plains\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(r.h.openSearch)
	r.searchReady(t)
	r.keys(t, decltest.Type("unrelatedquery")...)
	r.s.WaitForText(t, "hits (0) · lexical · semantic off")
	if _, err := d.db.AddProvider(context.Background(), store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.h.session.Call(context.Background(), "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "search cleared: the embedding model changed")
	r.s.WaitFor(t, "the field and the hits emptied", func(sc string) bool {
		return !strings.Contains(sc, "unrelatedquery") && !strings.Contains(sc, "hits (")
	})
	if q := onLoop(r, func() string { return r.h.searchQuery }); q != "" {
		t.Fatalf("the query after a model change is %q, want none", q)
	}
	r.s.WaitFor(t, "the mark naming the model", func(string) bool { return r.markOf() == "green semantic search · embedder" })
}

func TestPartialEmbeddingExplainsEmptyHitsUntilSearchRefreshes(t *testing.T) {
	o := newFakeOllama(t, "embedder")
	release := o.hold()
	defer release()
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# Wildlife\n\nzebra plains\n")})
	if _, err := d.db.AddProvider(context.Background(), store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	if _, err := r.h.session.Call(context.Background(), "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	// the provider in use before the query, so the poll's partial state below is the same model's
	// and refreshes the query rather than clearing it (a model change clears it)
	r.s.WaitFor(t, "the TUI polled the provider in use", func(string) bool {
		return onLoop(r, func() bool { return r.h.prog.emb.on && r.h.prog.emb.model != "" })
	})
	model := onLoop(r, func() string { return r.h.prog.emb.model })
	r.h.p.Post(r.h.openSearch)
	r.searchReady(t)
	r.keys(t, decltest.Type("unrelatedquery")...)
	// Hold the provider request, then deliver the poll's partial state. The
	// notification must precede the blocked search RPC's answer.
	r.h.p.Post(func() {
		r.h.showProgress(1, 0, embedProgress{on: true, model: model, semantic: "partial", texts: 1, pending: 1}, 1)
	})
	r.s.WaitForText(t, "search in kb is waiting: 1 texts still embedding")
	release()
	r.h.p.Post(r.h.poll)
	r.s.WaitFor(t, "the same query gains a semantic result", func(sc string) bool { return strings.Contains(sc, "hits (1) · hybrid") })
}

func TestClearingAndClosingAWaitingSearchCancelsItsRequest(t *testing.T) {
	o := newFakeOllama(t, "embedder")
	release := o.hold()
	defer release()
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# Wildlife\n\nzebra plains\n")})
	if _, err := d.db.AddProvider(context.Background(), store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(r.h.openSearch)
	r.s.WaitForText(t, "search: words")
	if _, err := r.h.session.Call(context.Background(), "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() {
		r.h.showProgress(1, 0, embedProgress{on: true, semantic: "partial", texts: 1, pending: 1}, 1)
		r.h.searchLive("unrelatedquery")
	})
	r.s.WaitForText(t, "search in kb is waiting: 1 texts still embedding")
	r.h.p.Post(func() { r.h.searchLive("") })
	r.s.WaitForText(t, "search cleared")
	if count := onLoop(r, func() int { return len(r.h.hitList) }); count != 0 {
		t.Errorf("clearing search left %d hits", count)
	}
	r.h.p.Post(func() { r.h.searchLive("unrelatedquery") })
	r.s.WaitForText(t, "search in kb is waiting: 1 texts still embedding")
	r.h.p.Post(r.h.searchClosed)
	r.s.WaitForText(t, "search closed")
	if active := onLoop(r, func() bool { return r.h.searchCancel != nil || r.h.searchWaitToast }); active {
		t.Error("a closed search still holds a request or ongoing toast")
	}
	release()
}

func TestSemanticQueryErrorNotifiesOfWordsOnlyFallback(t *testing.T) {
	o := newFakeOllama(t, "embedder")
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# Wildlife\n\nzebra plains\n")})
	if _, err := d.db.AddProvider(context.Background(), store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	if _, err := r.h.session.Call(context.Background(), "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "● semantic search")
	o.mu.Lock()
	o.down = true
	o.mu.Unlock()
	r.h.p.Post(r.h.openSearch)
	r.searchReady(t)
	r.keys(t, decltest.Type("zebra")...)
	// the answer to "zebra" itself, not to "zebr", which a key typed later supersedes but whose
	// answer can apply first (no lexical hit, the same fallback notice)
	r.s.WaitFor(t, "zebra's own answer, by words only", func(_ string) bool {
		return onLoop(r, func() bool {
			return r.h.searchQuery == "zebra" && r.h.searchCancel == nil &&
				r.h.lastSearchError == "semantic search could not answer in kb; results are by words only"
		})
	})
	if hits := onLoop(r, func() int { return len(r.h.hitList) }); hits == 0 {
		t.Error("query error dropped lexical results")
	}
}

func TestModelSwitchExplainsTemporaryWordsOnlyAndClearsSearch(t *testing.T) {
	o := newFakeOllama(t, "first", "second")
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# Wildlife\n\nzebra plains\n")})
	for _, model := range []string{"first", "second"} {
		if _, err := d.db.AddProvider(context.Background(), store.ProviderSpec{Name: model, Kind: store.KindOllama, BaseURL: o.URL, Model: model}); err != nil {
			t.Fatal(err)
		}
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	if _, err := r.h.session.Call(context.Background(), "embedding.use", "first"); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(r.h.openSearch)
	r.searchReady(t)
	r.keys(t, decltest.Type("unrelatedquery")...)
	r.s.WaitFor(t, "semantic hit before switch", func(sc string) bool { return strings.Contains(sc, "hits (1) · hybrid") })
	release := o.hold()
	defer release()
	if _, err := r.h.session.Call(context.Background(), "embedding.use", "second"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "switching said, the search cleared", func(sc string) bool {
		return strings.Contains(sc, "temporarily words-only") && strings.Contains(sc, "search cleared: the embedding model changed")
	})
	if q := onLoop(r, func() string { return r.h.searchQuery }); q != "" {
		t.Fatalf("the query after a switch began is %q, want none", q)
	}
	r.s.WaitFor(t, "the mark naming the model it switches to", func(string) bool { return r.markOf() == "red lexical search · switching to second" })
	release()
	r.s.WaitFor(t, "the mark naming the new model", func(string) bool { return r.markOf() == "green semantic search · second" })
}

func TestCanceledSwitchDoesNotClaimSemanticSearchReturned(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "alpha\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() {
		r.h.showProgress(1, 0, embedProgress{on: true, model: "old", target: "new", semantic: "switching", texts: 2, targetPending: 1}, 1)
		r.h.showProgress(1, 0, embedProgress{}, 1)
	})
	r.s.WaitForText(t, "model switch ended; search in kb remains words-only")
}

func TestWorkspaceSwitchReplacesSearchResultsWithoutEditingQuery(t *testing.T) {
	d := startManaged(t, map[string]string{
		"alpha": fileDir(t, "a.md", "# A\n\nzebra plains\n"),
		"bravo": fileDir(t, "b.md", "# B\n\nriver mud\n"),
	})
	r := runTUI(t, NewSession(d.sock, nil), Options{Workspace: "alpha"})
	r.s.WaitForText(t, "· alpha")
	r.h.p.Post(r.h.openSearch)
	r.searchReady(t)
	r.keys(t, decltest.Type("zebra")...)
	r.s.WaitForText(t, "hits (1) · lexical")
	r.h.p.Post(func() { r.h.enter("bravo") })
	r.s.WaitFor(t, "the other workspace's query result", func(sc string) bool {
		return strings.Contains(sc, "zebra") && strings.Contains(sc, "hits (0) · lexical") && !strings.Contains(sc, "a.md")
	})
}

func TestSearchFailureClearsStaleHitsAndSaysWhy(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\nzebra plains\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(r.h.openSearch)
	r.searchReady(t)
	r.keys(t, decltest.Type("zebra")...)
	r.s.WaitForText(t, "hits (1) · lexical")
	r.h.p.Post(func() { r.h.ws = "missing"; r.h.searchLive("zebra") })
	r.s.WaitForText(t, "search in missing is unavailable")
	if count := onLoop(r, func() int { return len(r.h.hitList) }); count != 0 {
		t.Fatalf("%d hits survived a failed search", count)
	}
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
	held chan struct{} // set: an embed of anything but the probe waits until it closes
}

// hold makes the embeds wait (the probe answers) until the returned release.
func (f *fakeOllama) hold() (release func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{})
	f.held = ch
	return sync.OnceFunc(func() { close(ch) })
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
			down, held := f.down, f.held
			f.mu.Unlock()
			if down {
				http.Error(w, "usage limit", http.StatusTooManyRequests)
				return
			}
			var req struct{ Input []string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			if held != nil && !(len(req.Input) == 1 && req.Input[0] == "probe") {
				select {
				case <-held:
				case <-r.Context().Done():
					return
				}
			}
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
	root := fileDir(t, "a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n")
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
	r.s.WaitFor(t, "the status line's red mark", func(string) bool { return r.semanticMark() == "red lexical search" })
	r.leader(t, 'a') // System › AI models
	r.s.WaitForText(t, "semantic search off")
	r.keys(t, key('u')) // the first row: local
	r.s.WaitForText(t, "semantic search with local")
	r.s.WaitFor(t, "the status line's green mark", func(string) bool { return r.semanticMark() == "green semantic search" })
	// each Ollama provider's context window, in its column
	r.s.WaitFor(t, "the context column", func(sc string) bool {
		for _, row := range strings.Split(sc, "\n") {
			if strings.Contains(row, "local") && strings.Contains(row, "embedder") && strings.Contains(row, "8192") {
				return true
			}
		}
		return false
	})
	r.s.WaitFor(t, "the dialog's green mark", func(string) bool { return r.markAbove(1) == "green semantic search · embedder" })
	// its calls, metered and written every couple of seconds, under the list
	r.s.WaitFor(t, "local's usage", func(sc string) bool {
		r.h.p.Post(func() { r.h.providerDetail(0) })
		return strings.Contains(sc, "requests ·") && strings.Contains(sc, "latest calls") && strings.Contains(sc, "· ok")
	})
	// at its usage limit: a new file's embedding is refused, and the log says why
	ollama.mu.Lock()
	ollama.down = true
	ollama.mu.Unlock()
	if err := os.WriteFile(filepath.Join(root, "c.md"), []byte("# C\n\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the refused call", func(sc string) bool {
		r.h.p.Post(func() { r.h.providerDetail(0) })
		text := strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " ") // wrapped: running text
		return strings.Contains(text, "at the usage limit") && strings.Contains(text, "rate limited")
	})
	r.s.WaitFor(t, "the mark red while the provider fails, the dialog saying why", func(string) bool {
		return r.semanticMark() == "red lexical search" && r.markAbove(1) == "red lexical search · the provider is not answering"
	})
	if snap := strings.Split(r.s.String(), "\n"); strings.Contains(snap[len(snap)-1], "not answering") {
		t.Fatalf("the status line carries the reason, which the left segment needs the room for:\n%s", r.s)
	}

	// a provider that does not set up: refused, and local stays in use
	r.h.p.Post(func() { r.h.useProvider(1) })
	r.s.WaitFor(t, "missing refused", func(sc string) bool {
		// the reason, not the model's name in the list, wrapped: read as running text
		text := strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " ")
		return strings.Contains(text, "missing not used: the provider has no such model")
	})
	if got := onLoop(r, func() string { return r.h.activeProvider }); got != "local" {
		t.Fatalf("after a refused Use the provider in use is %q", got)
	}

	r.keys(t, key('w')) // Words only
	r.s.WaitForText(t, "semantic search off")
	r.s.WaitFor(t, "the mark red with none in use", func(string) bool { return r.semanticMark() == "red lexical search" })
	r.s.WaitFor(t, "the dialog's red mark", func(string) bool { return r.markAbove(1) == "red lexical search" })
	r.keys(t, key('r')) // Remove…, the first row
	r.s.WaitForText(t, "remove the provider?")
	r.s.WaitForText(t, "Remove the provider local?")
	r.keys(t, key('y'))
	// the list, not the status line: a scan ending ("indexed 3 files") can replace the message
	r.s.WaitFor(t, "local gone from the store and the list", func(sc string) bool {
		ps, err := d.db.Providers(ctx)
		return err == nil && len(ps) == 1 && !strings.Contains(sc, "local           Ollama")
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
	bad := [][]qml.SpecValue{{b}, {b, b}, {b, b, b, b}, {frac}, {frac, s}, {s, s, s, b}, {s, s, s, s, b}}
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

// TestAListingThatFailsSaysSo: the workspace's files not listed (a workspace gone from the daemon)
// is on the status line, and the pickers list nothing.
func TestAListingThatFailsSaysSo(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.h.p.Post(func() { r.h.enter("gone") })
	r.s.WaitForText(t, "files: ")
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
	r.waitFile(t, "a.md")
	at := onLoop(r, func() [2]int { l, c := r.h.editor.Line(); return [2]int{l, c} })
	line := onLoop(r, func() string { return r.h.editor.Lines()[at[0]] })
	if !strings.HasPrefix(line, "kestrel") && !strings.HasPrefix(line, "## Birds") || at[1] != 0 {
		t.Errorf("the cursor opened at line %d col %d (%q), not at the hit's start", at[0], at[1], line)
	}
}

// TestTheBlankPageIsADraft: with no file open the page takes typing, as an untitled draft marked
// unsaved; Ctrl+S names it in the new-note picker and creates the file with its text, open, the
// cursor where it was. Closing the picker keeps the draft.
func TestTheBlankPageIsADraft(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.typeInEditor(t, "first thoughts\nsecond line")
	r.s.WaitFor(t, "the draft unsaved", func(sc string) bool { return strings.Contains(sc, "untitled [+]") })
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "save the draft")
	r.keys(t, esc()) // closed without a name: the draft stays, unsaved
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "save the draft") })
	if got := r.editorText(); got != "first thoughts\nsecond line" || !r.file().dirty {
		t.Fatalf("after closing the picker: %q, dirty %v", got, r.file().dirty)
	}
	before := onLoop(r, func() [2]int { l, c := r.h.editor.Line(); return [2]int{l, c} })
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "save the draft")
	r.keys(t, decltest.Type("ideas/draft")...)
	r.keys(t, enter())
	r.waitFile(t, "ideas/draft.md")
	if got := d.read(t, "kb", "ideas/draft.md"); got != "first thoughts\nsecond line" {
		t.Fatalf("the note holds %q, want the draft", got)
	}
	if after := onLoop(r, func() [2]int { l, c := r.h.editor.Line(); return [2]int{l, c} }); after != before {
		t.Errorf("the cursor moved from %v to %v when the draft became a note", before, after)
	}
}

// TestADraftIsGuardedLikeAFile: opening a file over an unsaved draft asks; Save names the draft,
// creates it, and then opens the file asked for.
func TestADraftIsGuardedLikeAFile(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.typeInEditor(t, "draft text")
	r.openByPicker(t, "a.md")
	r.s.WaitForText(t, "untitled has unsaved changes")
	r.keys(t, key('s'))
	r.s.WaitForText(t, "save the draft")
	r.keys(t, decltest.Type("kept")...)
	r.keys(t, enter())
	r.waitFile(t, "a.md") // the open it guarded, after the save
	if got := d.read(t, "kb", "kept.md"); got != "draft text" {
		t.Fatalf("kept.md holds %q, want the draft", got)
	}
}

// TestTheExplorerStaysOpenWhenNothingChanged: a scan's end lists the workspace's files again, and
// a connect, a create or a manager change lists the workspaces again; with the same files and the
// same workspaces, the rows open stay open. A note added is a change, and is listed with nothing
// asked.
func TestTheExplorerStaysOpenWhenNothingChanged(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n", "dir/b.md", "# B\n"}})
	r := attached(t, d)
	r.leader(t, 'e')
	r.s.WaitForText(t, "kb")
	r.keys(t, enter()) // kb
	r.s.WaitForText(t, "dir/")
	r.keys(t, key('j'), enter()) // dir/
	r.s.WaitForText(t, "b.md")
	settled := func(what string) {
		t.Helper()
		time.Sleep(200 * time.Millisecond) // the lists' answers, off the loop, have come
		if sc := r.s.String(); !strings.Contains(sc, "b.md") {
			t.Fatalf("after %s the open folder closed:\n%s", what, sc)
		}
	}
	r.h.p.Post(func() { r.h.relistInExplorer("kb") })
	settled("a scan's end")
	r.h.p.Post(func() { r.h.loadWorkspaces() })
	settled("the workspaces listed again")
	// a file added outside the TUI, indexed between two polls: the change log moved, so the files
	// are listed again, the pickers' and the explorer's, with nothing asked
	d.write(t, "kb", "c.md", "# C\n")
	r.waitListed(t, 3)
	r.s.WaitForText(t, "c.md")
}

// TestTheOptionsMenu: the editor mode, the theme and the editor's preferences are under Options; the AI
// models and the backend's restart under System, right of Options; File keeps the file's commands.
func TestTheOptionsMenu(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.keys(t, decltest.Alt('o'))
	r.s.WaitFor(t, "the Options menu", func(sc string) bool {
		return strings.Contains(sc, "Editor mode") && strings.Contains(sc, "Editor preferences…")
	})
	if sc := r.s.String(); strings.Contains(sc, "AI models…") || strings.Contains(sc, "Restart backend…") {
		t.Fatalf("Options still has the system's items:\n%s", sc)
	}
	bar := strings.Split(r.s.String(), "\n")[0]
	if o, s := strings.Index(bar, "Options"), strings.Index(bar, "System"); o < 0 || s < o {
		t.Fatalf("System is not right of Options on the menu bar: %q", bar)
	}
	r.keys(t, esc(), esc())
	r.keys(t, decltest.Alt('s'))
	r.s.WaitFor(t, "the System menu", func(sc string) bool {
		return strings.Contains(sc, "AI models…") && strings.Contains(sc, "Restart backend…") && !strings.Contains(sc, "Editor mode")
	})
	r.keys(t, key('a')) // AI models
	r.s.WaitForText(t, "embedding providers")
	r.keys(t, esc())
	r.s.WaitFor(t, "AI models closed", func(sc string) bool { return !strings.Contains(sc, "embedding providers") })
	r.keys(t, decltest.Alt('f'))
	r.s.WaitForText(t, "Reload from disk")
	if sc := r.s.String(); strings.Contains(sc, "Preferences…") || strings.Contains(sc, "Restart backend…") {
		t.Fatalf("File still has Preferences or the restart:\n%s", sc)
	}
}

// TestTheTextKeymap: in the Text keymap the page types as an ordinary editor does — no i, Space a
// space — Ctrl+Space opens the leader card, and Ctrl+S saves; the keymap is kept.
func TestTheTextKeymap(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.leader(t, 'k') // Vim to Text
	r.s.WaitForText(t, "editor mode: Text (modeless)")
	r.keys(t, decltest.Type("hi there ")...)
	r.s.WaitFor(t, "typed with no i, the space a space", func(string) bool { return strings.HasPrefix(r.editorText(), "hi there ") })
	r.keys(t, decltest.Ctrl(' '))
	r.s.WaitForText(t, "SPC — commands")
	r.keys(t, esc())
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "SPC — commands") })
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saved a.md")
	if got := d.read(t, "kb", "a.md"); got != "hi there aaa\n" {
		t.Fatalf("a.md holds %q", got)
	}
	r.s.WaitFor(t, "the keymap kept", func(string) bool {
		m, _ := d.db.Preferences(context.Background())
		return m["tui.editor.keymap"] == "text"
	})
}

// TestTheLeaderCardListsACommandARow: the card's commands are on rows of their own, two columns
// of them, not run together on one line.
func TestTheLeaderCardListsACommandARow(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.keys(t, key(' '))
	r.s.WaitForText(t, "SPC — commands")
	search, open, quit := screenRow(r, "search"), screenRow(r, "open a file"), screenRow(r, "quit")
	if !(search >= 0 && open == search+1 && quit > open) {
		t.Fatalf("rows: search %d, open a file %d, quit %d; want one command a row\n%s", search, open, quit, r.s)
	}
}

// screenRow is the screen row holding text, -1 when none does.
func screenRow(r *running, text string) int {
	for i, row := range strings.Split(r.s.String(), "\n") {
		if strings.Contains(row, text) {
			return i
		}
	}
	return -1
}

// TestThePagesWidthAppliesAsTyped: in the editor's preferences, a width typed is the page's at
// once, with no Enter; a width out of range says so and changes nothing.
func TestThePagesWidthAppliesAsTyped(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 160, 30)
	r.ready(t)
	r.leader(t, ',')
	r.s.WaitForText(t, "editor preferences")
	r.keys(t, tab(), decltest.Ctrl('u')) // past the keymap, into the width
	r.keys(t, decltest.Type("80")...)
	r.s.WaitFor(t, "the page 82 wide", func(string) bool { return onLoop(r, func() int { return r.h.prefs.ruler }) == 80 })
	r.keys(t, decltest.Type("0")...) // 800: out of range
	r.s.WaitForText(t, "40 to 400")
	if w := onLoop(r, func() int { return r.h.prefs.ruler }); w != 80 {
		t.Fatalf("an out-of-range width changed the page to %d", w)
	}
}

// TestTheAIModelsSideBySide: the providers on the left, the usage and calls on the right, on the
// same rows.
func TestTheAIModelsSideBySide(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "a\n")})
	if _, err := d.db.AddProvider(context.Background(), store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: "http://127.0.0.1:1", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.leader(t, 'a')
	r.s.WaitFor(t, "both panes", func(sc string) bool {
		// "no calls" alone: the pane wraps the rest, and the panes' rows interleave
		return strings.Contains(sc, "embedding providers") && strings.Contains(sc, "usage and calls") && strings.Contains(sc, "no calls")
	})
	if p, u := screenRow(r, "embedding providers"), screenRow(r, "usage and calls"); p != u {
		t.Fatalf("the providers (row %d) and the usage (row %d) are not side by side:\n%s", p, u, r.s)
	}
}

// TestEveryPreferenceIsKept: each preference command changes the screen's preference and the
// store's; a name the TUI does not know changes nothing and says so.
func TestEveryPreferenceIsKept(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	stored := func(name, want string) {
		t.Helper()
		r.s.WaitFor(t, name+" = "+want, func(string) bool {
			m, err := d.db.Preferences(context.Background())
			return err == nil && m[name] == want
		})
	}
	post := func(fn func()) { r.h.p.Post(fn) }
	post(func() { r.h.setMenuHiddenIndex(1) }) // no: shown
	stored("tui.menu.autohide", "false")
	r.s.WaitForText(t, "the menu bar shows")
	post(func() { r.h.toggleMenuBar() })
	stored("tui.menu.autohide", "true")
	post(func() { r.h.setStatusShownIndex(1) }) // no: hidden
	stored("tui.status.shown", "false")
	post(func() { r.h.setExplorerEdge(2) })
	stored("tui.explorer.edge", "top")
	post(func() { r.h.setLinksEdge(3) })
	stored("tui.links.edge", "bottom")
	post(func() { r.h.setThemeIndex(1) })
	stored("tui.theme", "light")
	post(func() { r.h.setKeymapIndex(1) })
	stored("tui.editor.keymap", "text")
	post(func() { r.h.toggleKeymap() })
	stored("tui.editor.keymap", "vim")
	post(func() { r.h.setStatusShownIndex(0) }) // yes: shown, to read what follows
	stored("tui.status.shown", "true")
	post(func() { r.h.useTheme("paisley") })
	r.s.WaitForText(t, `no theme "paisley"`)
	post(func() { r.h.setKeymap("emacs") })
	r.s.WaitForText(t, `no editor mode "emacs"`)
	if p := onLoop(r, func() prefs { return r.h.prefs }); p.theme != "light" || p.keymap != "vim" {
		t.Fatalf("an unknown theme or keymap changed the preferences: %+v", p)
	}
	// the page and the notifications, from the dialog's choosers
	post(func() { r.h.setWrapIndex(1) }) // no
	stored("tui.editor.wrap", "false")
	post(func() { r.h.setLineNumbersIndex(0) }) // yes
	stored("tui.editor.linenumbers", "true")
	post(func() { r.h.setLineNumbersIndex(5) }) // no such row: nothing
	post(func() { r.h.setToastCorner(indexOf(corners, "top-right")) })
	stored("tui.toast.corner", "top-right")
	post(func() { r.h.setToastSeconds(9) }) // row 9: ten seconds
	stored("tui.toast.seconds", "10")
	post(func() { r.h.setToastSeconds(10) }) // eleven: out of range, nothing
	if p := onLoop(r, func() prefs { return r.h.prefs }); p.toastSeconds != 10 || !p.lineNumbers || p.wrap {
		t.Fatalf("after the choosers: %+v", p)
	}
	// the menu bar shown, the toasts at the top keep its row clear; hidden, they take it
	if m := onLoop(r, r.h.toastMargin); m != 0 {
		t.Errorf("the menu bar hidden, the top margin is %d, want 0", m)
	}
	post(func() { r.h.toggleMenuBar() })
	stored("tui.menu.autohide", "false")
	if m := onLoop(r, r.h.toastMargin); m != 1 {
		t.Errorf("the menu bar shown, the top margin is %d, want 1", m)
	}
}

// TestThePreferencesAreReadWithTheirDefaults: what the store keeps is read over the defaults; a
// value the TUI cannot use is the default's.
func TestThePreferencesAreReadWithTheirDefaults(t *testing.T) {
	p := prefsOf(map[string]any{"tui.editor.wrap": "false", "tui.editor.linenumbers": "true",
		"tui.toast.corner": "top-left", "tui.toast.seconds": "7"})
	if p.wrap || !p.lineNumbers || p.toastCorner != "top-left" || p.toastSeconds != 7 {
		t.Fatalf("read %+v", p)
	}
	d := prefsOf(map[string]any{"tui.toast.corner": "middle", "tui.toast.seconds": "11"})
	if !d.wrap || d.lineNumbers || d.toastCorner != "bottom-right" || d.toastSeconds != defaultToastSeconds {
		t.Fatalf("unusable values: %+v, want the defaults", d)
	}
	if z := prefsOf(map[string]any{"tui.toast.seconds": "0"}); z.toastSeconds != defaultToastSeconds {
		t.Errorf("zero seconds: %d", z.toastSeconds)
	}
}

// semanticMark is the status line's semantic-search mark: the dot's colour, then the words after
// it ("green semantic search"); "" when the bottom row has no dot.
func (r *running) semanticMark() string {
	snap := r.s.Backend.Snapshot()
	m := markIn(snap[len(snap)-1], "")
	// the mark sits at the right end, after the status: its words are one of the two labels
	colour, rest, _ := strings.Cut(m, " ")
	for _, label := range []string{"semantic search", "lexical search"} {
		if rest == label || strings.HasPrefix(rest, label+" ") {
			return colour + " " + label
		}
	}
	return m
}

// markOf is the status line's whole mark: the dot's colour and every word after it, the model
// included ("green semantic search · embedder").
func (r *running) markOf() string {
	snap := r.s.Backend.Snapshot()
	return markIn(snap[len(snap)-1], "")
}

// markAbove is the AI models dialog's mark: the first row above the status line with a dot
// followed by "semantic search" or "lexical search", read up to the frame's border.
func (r *running) markAbove(skip int) string {
	snap := r.s.Backend.Snapshot()
	for _, row := range snap[:len(snap)-skip] {
		if m := markIn(row, "│"); strings.HasSuffix(m, " search") || strings.Contains(m, " search ·") {
			return m
		}
	}
	return ""
}

// markIn is row's first dot's colour and the words after it, up to stop ("" for the row's end).
func markIn(row []tuicore.Cell, stop string) string {
	for x, c := range row {
		if c.Content != "●" {
			continue
		}
		colour := map[tuicore.CellColor]string{{Kind: tuicore.CellColorANSI, Index: 1}: "red", {Kind: tuicore.CellColorANSI, Index: 2}: "green"}[c.Attrs.FG]
		var rest strings.Builder
		for _, c := range row[x+1:] {
			if stop != "" && c.Content == stop {
				break
			}
			rest.WriteString(c.Content)
		}
		if m := strings.TrimSpace(rest.String()); colour != "" && m != "" {
			return colour + " " + m
		}
	}
	return ""
}

// TestRestartBringsUpTheInstalledBackend: System › Restart backend… asks, saying the version running
// and the one installed; yes stops the daemon, the reconnect waits for it to go, starts the
// installed one, and says so.
func TestRestartBringsUpTheInstalledBackend(t *testing.T) {
	dir, err := os.MkdirTemp("", "adr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	notes := map[string][]string{"kb": {"a.md", "a\n"}}
	old := startDaemonWith(t, sock, notes, daemonOpts{version: "v1"})
	var spawns atomic.Int32
	sess := NewSession(sock, func() (string, error) {
		// once: the new daemon's cleanup runs before the TUI's, whose reconnect must not start
		// another after the test
		if spawns.Add(1) > 1 {
			return "", errors.New("spawned already")
		}
		startDaemonWith(t, sock, notes, daemonOpts{version: "v2"})
		return "", nil
	})
	r := runTUI(t, sess, Options{Installed: func() (string, error) { return "v2", nil }})
	r.ready(t)
	onLoop(r, func() bool {
		r.h.awaitExit = func(ctx context.Context, _ int64) bool {
			select {
			case <-old.stopped:
				return true
			case <-ctx.Done():
				return false
			}
		}
		return true
	})
	if spawns.Load() != 0 {
		t.Fatal("a daemon was spawned with one answering")
	}
	r.h.p.Post(r.h.startRestart)
	r.s.WaitFor(t, "the question", func(sc string) bool {
		text := strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " ")
		return strings.Contains(text, "Restart the backend (autodoc v1)?") && strings.Contains(text, "as autodoc v2, the one installed")
	})
	r.keys(t, key('y'))
	r.s.WaitForText(t, "backend restarted: autodoc v1 → v2")
	select {
	case <-old.stopped:
	default:
		t.Fatal("the old daemon still runs")
	}
	if n := spawns.Load(); n != 1 {
		t.Fatalf("%d daemons spawned, want the one", n)
	}
	if v := onLoop(r, func() string { return r.h.session.Version() }); v != "v2" {
		t.Errorf("connected to %q, want the installed v2", v)
	}
}

// TestRestartNeedsASpawner: a TUI that starts no daemon offers no restart, since nothing would
// bring one back.
func TestRestartNeedsASpawner(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.h.p.Post(r.h.startRestart)
	r.s.WaitForText(t, "this TUI starts no daemon")
	select {
	case <-d.stopped:
		t.Fatal("the daemon stopped")
	default:
	}
}

// TestVectorsListsTheModelsAndPurgesAnUnusedOne: after a switch, AI models › Vectors… lists the new
// model active and the old one unused, each with the room its vectors take; Purge… on the active
// one is refused, on the unused one asks, then removes it. Cancel indexing with nothing embedding
// says so.
func TestVectorsListsTheModelsAndPurgesAnUnusedOne(t *testing.T) {
	ollama := newFakeOllama(t, "embedder", "other")
	root := fileDir(t, "a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n")
	d := startManaged(t, map[string]string{"kb": root})
	ctx := context.Background()
	if _, err := d.db.AddProvider(ctx, store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	settled := func(what string) {
		t.Helper()
		r.s.WaitFor(t, what, func(string) bool {
			e := onLoop(r, func() embedProgress { return r.h.prog.emb })
			return e.online() && e.texts > 0 && e.working() == 0
		})
	}
	if _, err := r.h.session.Call(ctx, "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	settled("embedder covering the notes")
	if _, err := r.h.session.Call(ctx, "embedding.update", "local",
		map[string]any{"name": "local", "kind": "ollama", "base_url": ollama.URL, "model": "other"}); err != nil {
		t.Fatal(err)
	}
	// the switch done: other the active model, and embedder, superseded, reclaimed (ADR 1791284787)
	r.s.WaitFor(t, "other active, embedder reclaimed", func(string) bool {
		v, _ := r.h.session.Call(ctx, "index.models", "kb")
		ms := asList(v)
		return len(ms) == 1 && str(asMap(ms[0]), "name") == "other" && str(asMap(ms[0]), "state") == "active"
	})
	settled("other covering the notes")
	r.h.p.Post(r.h.cancelIndexing)
	r.s.WaitForText(t, "nothing is being embedded")

	// a model no longer used, as one is while it is reclaimed: two 3-dimension vectors
	seedUnusedModel(t, d.db, "kb", "ollama|old|sha256:old|3", 2)
	r.h.p.Post(r.h.openVectors)
	r.s.WaitFor(t, "both models listed", func(sc string) bool {
		return regexp.MustCompile(`active +other +3 +2 `).MatchString(sc) && regexp.MustCompile(`unused +old +3 +2 `).MatchString(sc) &&
			strings.Contains(sc, "in models no longer used")
	})
	// two 3-dimension vectors: 24 bytes of float32, 16 of codes
	if sc := r.s.String(); !regexp.MustCompile(`unused +old +3 +2 +24 B +16 B `).MatchString(sc) {
		t.Fatalf("old's room:\n%s", sc)
	}
	r.h.p.Post(func() { r.h.startPurge(0) })
	r.s.WaitForText(t, "other is the active model: choose another model, or remove its provider in AI models")
	r.h.p.Post(func() { r.h.startPurge(1) })
	r.s.WaitForText(t, "purge the model?")
	r.keys(t, key('y'))
	r.s.WaitForText(t, "purged old's 2 vectors from kb")
	r.s.WaitFor(t, "old gone from the list", func(sc string) bool {
		return !strings.Contains(sc, "unused") && strings.Contains(sc, "1 models")
	})
}

// TestRemovingAProviderSaysWhatItDeletes (ADR 1791284787 §2.3, item 4): the removal's question
// says what goes. The provider in use, its model served by no other: semantic search off, and its
// vectors counted across the workspaces. Another provider of the same model: its vectors kept.
// Another provider in use: its vectors go once nothing uses them.
func TestRemovingAProviderSaysWhatItDeletes(t *testing.T) {
	ollama := newFakeOllama(t, "embedder", "other")
	root := fileDir(t, "a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n")
	d := startManaged(t, map[string]string{"kb": root})
	ctx := context.Background()
	for name, model := range map[string]string{"local": "embedder", "spare": "other"} {
		if _, err := d.db.AddProvider(ctx, store.ProviderSpec{Name: name, Kind: store.KindOllama, BaseURL: ollama.URL, Model: model}); err != nil {
			t.Fatal(err)
		}
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	if _, err := r.h.session.Call(ctx, "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "embedder covering the notes", func(string) bool {
		e := onLoop(r, func() embedProgress { return r.h.prog.emb })
		return e.online() && e.texts > 0 && e.working() == 0
	})
	// ask opens the removal's question for name, once the list holds listed too
	ask := func(name, listed string) {
		t.Helper()
		onLoop(r, func() bool { r.h.loadProviders(); return true })
		r.s.WaitFor(t, "the providers listed", func(string) bool {
			return onLoop(r, func() bool {
				return slices.ContainsFunc(r.h.providerList, func(p providerRow) bool { return p.name == listed })
			})
		})
		onLoop(r, func() bool {
			r.h.startRemoveProvider(slices.IndexFunc(r.h.providerList, func(p providerRow) bool { return p.name == name }))
			return true
		})
	}
	question := func() string {
		return onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.providerRemoveQuestion"); return v.Raw })
	}

	ask("local", "local")
	r.s.WaitFor(t, "the vectors counted", func(string) bool {
		return strings.Contains(question(), "Semantic search goes off, and embedder's 2 vectors (") &&
			strings.HasSuffix(question(), " in 1 workspaces are deleted.")
	})
	onLoop(r, func() bool { r.h.closeDialog("providerRemove"); return true })

	if _, err := d.db.AddProvider(ctx, store.ProviderSpec{Name: "twin", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	ask("local", "twin")
	r.s.WaitFor(t, "the twin keeps them", func(string) bool {
		return strings.HasSuffix(question(), "embedder keeps its vectors: twin serves it too.")
	})
	onLoop(r, func() bool { r.h.closeDialog("providerRemove"); return true })

	ask("spare", "spare")
	r.s.WaitFor(t, "another in use", func(string) bool {
		return strings.HasSuffix(question(), "Its model's vectors go once no workspace uses them.")
	})
}

// seedUnusedModel adds model fp to workspace name, neither active nor the target, with n vectors of
// 3 dimensions, through a raw connection to the store's file: the API writes none such.
func seedUnusedModel(t *testing.T, db *store.Store, name, fp string, n int) {
	t.Helper()
	ctx := context.Background()
	ws, err := db.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, w := range ws {
		if w.Name == name {
			id = w.ID
		}
	}
	raw, err := sqlite.OpenNamed(ctx, "seed:"+db.Path(), "file:"+db.Path()+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, "INSERT INTO model (workspace_id, fp, provider, name, dims, active, target) VALUES (?, ?, 'ollama', 'old', 3, 0, 0)", id, fp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := raw.ExecContext(ctx, "INSERT INTO embedding (workspace_id, text_hash, model_fp, bits, f32) VALUES (?, ?, ?, x'0000000000000000', x'000000000000000000000000')",
			id, []byte(fmt.Sprintf("old-%d", i)), fp); err != nil {
			t.Fatal(err)
		}
	}
}

// otherDaemon is a daemon of protocol proto at sock: its hello refuses any other, admits proto, and
// answers a probe with its number; sys.shutdown, admitted, stops it.
func otherDaemon(t *testing.T, sock string, proto int64) (stopped chan struct{}) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("sys.hello", func(_ context.Context, req *golibrpc.Request) (any, error) {
		reply := map[string]any{"protocol": proto, "version": fmt.Sprintf("v-p%d", proto), "pid": int64(os.Getpid())}
		m, _ := req.Params[0].(map[string]any)
		switch p, declared := m["protocol"]; {
		case !declared:
			return reply, nil
		case p != proto:
			return nil, &golibrpc.Error{Code: rpc.CodeProtocolMismatch, Message: fmt.Sprintf("protocol mismatch: client %v, server %d", p, proto)}
		}
		req.Session.SetValue("hello", true)
		return reply, nil
	})
	srv.Handle("sys.shutdown", func(_ context.Context, req *golibrpc.Request) (any, error) {
		if ok, _ := req.Session.Value("hello").(bool); !ok {
			return nil, &golibrpc.Error{Code: rpc.CodeHandshakeRequired, Message: "handshake required"}
		}
		go cancel()
		return nil, nil
	})
	stopped = make(chan struct{})
	go func() { _ = srv.Run(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped })
	return stopped
}

// TestRestartReplacesAnOlderBackend: a daemon of an older protocol refuses this TUI's hello; the
// status line says it is older and how to replace it, and Restart backend… stops it over its own
// protocol and starts the installed one.
func TestRestartReplacesAnOlderBackend(t *testing.T) {
	dir, err := os.MkdirTemp("", "ado")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	old := otherDaemon(t, sock, rpc.Protocol-1)
	var spawns atomic.Int32
	sess := NewSession(sock, func() (string, error) {
		if spawns.Add(1) > 1 {
			return "", errors.New("spawned already")
		}
		startDaemonWith(t, sock, map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{version: "v2"})
		return "", nil
	})
	r := runTUI(t, sess, Options{Installed: func() (string, error) { return "v2", nil }})
	r.waitNoticed(t, fmt.Sprintf("the backend is autodoc v-p%d (protocol %d), older than this TUI", rpc.Protocol-1, rpc.Protocol-1))
	r.s.WaitFor(t, "mismatch recovery choices", func(screen string) bool {
		return strings.Contains(screen, "backend version mismatch") && strings.Contains(screen, "Restart Now") && strings.Contains(screen, "Quit")
	})
	onLoop(r, func() bool {
		r.h.awaitExit = func(ctx context.Context, _ int64) bool {
			select {
			case <-old:
				return true
			case <-ctx.Done():
				return false
			}
		}
		return true
	})
	r.h.p.Post(r.h.startRestart)
	r.s.WaitFor(t, "the question", func(sc string) bool {
		text := strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " ")
		return strings.Contains(text, fmt.Sprintf("Restart the backend (autodoc v-p%d)?", rpc.Protocol-1)) && strings.Contains(text, "as autodoc v2")
	})
	r.keys(t, key('y'))
	r.s.WaitForText(t, fmt.Sprintf("backend restarted: autodoc v-p%d → v2", rpc.Protocol-1))
	if n := spawns.Load(); n != 1 {
		t.Fatalf("%d daemons spawned, want the one", n)
	}
}

// TestTheSpinnerTurnsWhileTheModelEmbeds: while the provider embeds, the status line's bar has a
// spinner beside it that turns; a model switch shows the new model's fill and a red mark saying
// the search is by words meanwhile; once the fill is done, the mark is green again.
func TestTheSpinnerTurnsWhileTheModelEmbeds(t *testing.T) {
	ollama := newFakeOllama(t, "embedder", "other")
	root := fileDir(t, "a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n")
	d := startManaged(t, map[string]string{"kb": root})
	ctx := context.Background()
	if _, err := d.db.AddProvider(ctx, store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	release := ollama.hold()
	if _, err := r.h.session.Call(ctx, "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	frame := regexp.MustCompile(`embedding kb ([-\\|/]) ░+ 0/2`)
	seen := map[string]bool{}
	r.s.WaitFor(t, "the spinner turning", func(sc string) bool {
		if m := frame.FindStringSubmatch(sc); m != nil {
			seen[m[1]] = true
		}
		return len(seen) >= 2
	})
	release()
	r.s.WaitFor(t, "embedder done", func(string) bool {
		return r.semanticMark() == "green semantic search" && !strings.Contains(r.s.String(), "embedding ")
	})
	release = ollama.hold()
	defer release()
	if _, err := r.h.session.Call(ctx, "embedding.update", "local",
		map[string]any{"name": "local", "kind": "ollama", "base_url": ollama.URL, "model": "other"}); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the switch on the status line", func(sc string) bool {
		return r.semanticMark() == "red lexical search" && regexp.MustCompile(`switching kb to other [-\\|/] ░+ 0/2`).MatchString(sc)
	})
	release()
	r.s.WaitFor(t, "other answering", func(sc string) bool {
		return r.semanticMark() == "green semantic search" && !strings.Contains(sc, "switching kb to")
	})
}

// TestANewerBackendSaysTheTUIIsOlder: a daemon of a newer protocol refuses this TUI, which says it
// is the older one and offers no restart (it would start this, older, build).
func TestANewerBackendSaysTheTUIIsOlder(t *testing.T) {
	dir, err := os.MkdirTemp("", "adn")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	newer := otherDaemon(t, sock, rpc.Protocol+1)
	r := runTUI(t, NewSession(sock, func() (string, error) { return "", errors.New("not in this test") }), Options{})
	r.s.WaitForText(t, fmt.Sprintf("this TUI (protocol %d) is older than the backend", rpc.Protocol))
	r.s.WaitFor(t, "newer backend cannot be restarted", func(screen string) bool {
		return strings.Contains(screen, "backend version mismatch") && strings.Contains(screen, "Quit") && !strings.Contains(screen, "Restart Now")
	})
	r.h.p.Post(r.h.startRestart)
	r.s.WaitForText(t, "restart: not connected to a backend")
	select {
	case <-newer:
		t.Fatal("the newer daemon was stopped")
	default:
	}
}

func TestOlderBackendWithoutSpawnOnlyOffersQuit(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	otherDaemon(t, sock, rpc.Protocol-1)
	running := runTUI(t, NewSession(sock, nil), Options{})
	running.s.WaitFor(t, "restart unavailable", func(screen string) bool {
		return strings.Contains(screen, "backend version mismatch") && strings.Contains(screen, "Quit") && !strings.Contains(screen, "Restart Now")
	})
}

// TestFindInThePanes: / asks for a word and finds it in the pane with the keyboard. In the page
// the cursor goes to it, n to the next, N back, wrapping. In the explorer the cursor goes to the
// row holding it. The workspace's search is SPC SPC (and SPC /, Ctrl+G), not /.
func TestFindInThePanes(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one kestrel\ntwo\nthree kestrel\nfour\n", "b.md", "see [[a]]\n",
		"kestrels/c.md", "c\n", "x.md", "also [[a]]\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	line := func() int { return onLoop(r, func() int { l, _ := r.h.editor.Line(); return l }) }
	r.keys(t, key('/'))
	r.s.WaitForText(t, "find in the page — n next, N previous")
	r.keys(t, decltest.Type("KESTREL")...) // case-blind
	r.keys(t, enter())
	r.s.WaitForText(t, `find "KESTREL": 1 of 2 in the page`)
	if l := line(); l != 0 {
		t.Fatalf("the first find put the cursor on line %d, want 0", l)
	}
	if c := onLoop(r, func() int { _, c := r.h.editor.Line(); return c }); c != 4 {
		t.Errorf("the cursor at column %d, want 4 (the word's start)", c)
	}
	r.keys(t, key('n'))
	r.s.WaitForText(t, `find "KESTREL": 2 of 2 in the page`)
	if l := line(); l != 2 {
		t.Fatalf("n put the cursor on line %d, want 2", l)
	}
	r.keys(t, key('n')) // wraps
	r.s.WaitForText(t, `find "KESTREL": 1 of 2 in the page`)
	r.keys(t, key('N')) // back, wrapping
	r.s.WaitForText(t, `find "KESTREL": 2 of 2 in the page`)
	if l := line(); l != 2 {
		t.Fatalf("N put the cursor on line %d, want 2", l)
	}
	// not there: said, the cursor where it was
	r.keys(t, key('/'))
	r.s.WaitForText(t, "find in the page")
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type("osprey")...)
	r.keys(t, enter())
	r.s.WaitForText(t, `find: no "osprey" in the page`)
	if l := line(); l != 2 {
		t.Fatalf("a find with no match moved the cursor to line %d", l)
	}

	// the explorer: the row holding it
	r.leader(t, 'e')
	r.s.WaitForText(t, "explorer")
	r.keys(t, enter()) // kb opens: its folder and its files load
	r.s.WaitForText(t, "kestrels/")
	r.keys(t, key('/'))
	r.s.WaitForText(t, "find in the explorer")
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type("b.md")...)
	r.keys(t, enter())
	r.s.WaitForText(t, `find "b.md": 1 of 1 in the explorer`)
	if at := onLoop(r, func() []string { return r.h.explorerAt }); len(at) == 0 || !strings.HasSuffix(at[len(at)-1], "b.md") {
		t.Fatalf("the explorer's cursor is on %q, want b.md", at)
	}
	r.keys(t, esc())
	r.s.WaitFor(t, "the explorer closed", func(sc string) bool { return !strings.Contains(sc, "┌ explorer") })

	// the links: the row holding it, the view's cursor moved there
	r.leader(t, 'l')
	r.s.WaitForText(t, "backlinks (2)")
	r.keys(t, key('/'))
	r.s.WaitForText(t, "find in the links")
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type("x.md")...)
	r.keys(t, enter())
	r.s.WaitForText(t, `find "x.md": 1 of 1 in the links`)
	if at := onLoop(r, func() int { return r.h.linksAt }); at != 1 {
		t.Fatalf("the links' cursor is on row %d, want 1 (x.md)", at)
	}
	r.keys(t, enter()) // Enter on the row found opens it: the view's own cursor is there
	r.waitFile(t, "x.md")

	// SPC SPC is the workspace's search
	r.keys(t, key(' '))
	r.s.WaitForText(t, "SPC — commands")
	r.keys(t, key(' '))
	r.s.WaitForText(t, "words; a * ends a prefix")
}

// waitNoticed waits for a toast holding text. A toast wraps a long message, so the screen's words
// are read as one line, the toast's borders dropped. Only for a screen whose page beside the toast
// is blank: a page's words there would come between the toast's rows.
func (r *running) waitNoticed(t *testing.T, text string) {
	t.Helper()
	r.s.WaitFor(t, "the notification "+text, func(sc string) bool {
		flat := strings.Join(strings.Fields(strings.NewReplacer("│", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ", "─", " ").Replace(sc)), " ")
		return strings.Contains(flat, text)
	})
}

// TestNotificationsAndTheStatusLine: what happened is a toast, over the status line at the bottom
// right, kept in the history (SPC h). A find's result is the status line's alone: no toast, not
// kept. The corner and the time a toast stays are preferences.
func TestNotificationsAndTheStatusLine(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel one\nkestrel two\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.h.p.Post(func() { r.h.notify("the index is ready") })
	r.s.WaitFor(t, "the toast at the bottom right", func(sc string) bool {
		rows := strings.Split(sc, "\n")
		n := len(rows)
		return strings.Contains(rows[n-3], "the index is ready") && strings.HasSuffix(strings.TrimRight(rows[n-3], " "), "│") &&
			strings.HasPrefix(rows[n-1], "NORMAL")
	})
	// a find: the status line says it, and nothing else does
	r.keys(t, key('/'))
	r.s.WaitForText(t, "find in the page")
	r.keys(t, decltest.Type("kestrel")...)
	r.keys(t, enter())
	r.s.WaitFor(t, "the find on the status line", func(sc string) bool {
		rows := strings.Split(sc, "\n")
		return strings.Contains(rows[len(rows)-1], `find "kestrel": 1 of 2 in the page`)
	})
	kept := onLoop(r, func() []string {
		var out []string
		for _, n := range r.h.notices {
			out = append(out, n.text)
		}
		return out
	})
	for _, k := range kept {
		if strings.Contains(k, "find") {
			t.Fatalf("a find's result was kept as a notification: %q", kept)
		}
	}
	if !slices.Contains(kept, "the index is ready") {
		t.Fatalf("the notification was not kept: %q", kept)
	}
	// the history, newest first
	r.leader(t, 'h')
	r.s.WaitFor(t, "the history", func(sc string) bool {
		return strings.Contains(sc, "notifications (") && strings.Contains(sc, "the index is ready") && strings.Contains(sc, "NOTIFICATION")
	})
	r.keys(t, key('l')) // Clear
	r.s.WaitForText(t, "notifications (0)")
	r.keys(t, key('q'))
	r.s.WaitFor(t, "the history closed", func(sc string) bool { return !strings.Contains(sc, "NOTIFICATION") })
	// the top left, staying 5 seconds: kept, and applied
	r.h.p.Post(func() {
		r.h.setToastCorner(indexOf(corners, "top-left"))
		r.h.setToastSeconds(4)
	})
	stored := func(name, want string) {
		t.Helper()
		r.s.WaitFor(t, name+" = "+want, func(string) bool {
			m, err := d.db.Preferences(context.Background())
			return err == nil && m[name] == want
		})
	}
	stored("tui.toast.corner", "top-left")
	stored("tui.toast.seconds", "5")
	r.h.p.Post(func() { r.h.notify("up here") })
	r.s.WaitFor(t, "the toast at the top left", func(sc string) bool {
		rows := strings.Split(sc, "\n")
		for _, row := range rows[:5] {
			if strings.HasPrefix(row, "│ up here") {
				return true
			}
		}
		return false
	})
}

// TestThePageWrapsAndNumbersItsLines: long lines wrap by default; View › Wrap long lines turns it
// off and on; line numbers show in a gutter when asked, the preference kept.
func TestThePageWrapsAndNumbersItsLines(t *testing.T) {
	long := strings.Repeat("word ", 40)
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "short\n" + long + "\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	words := func(sc string) int { return strings.Count(sc, "word") }
	r.s.WaitFor(t, "the long line wrapped", func(sc string) bool { return words(sc) == 40 })
	r.h.p.Post(r.h.toggleWrap)
	r.s.WaitFor(t, "the long line cut at the page", func(sc string) bool { return words(sc) < 40 })
	r.h.p.Post(r.h.toggleWrap)
	r.s.WaitFor(t, "wrapped again", func(sc string) bool { return words(sc) == 40 })
	r.h.p.Post(r.h.toggleLineNumbers)
	r.s.WaitFor(t, "numbered", func(sc string) bool { return strings.Contains(sc, "   1  short") && strings.Contains(sc, "   2  word") })
	// the cursor wears the theme's accent: dark's amber, sent to the terminal
	r.s.WaitFor(t, "the cursor's accent", func(string) bool {
		c, ok := r.s.Backend.CursorColor()
		return ok && c == tuicore.CellColor{Kind: tuicore.CellColorRGB, R: 0xff, G: 0xaf, B: 0x00}
	})
	r.s.WaitFor(t, "kept", func(string) bool {
		m, err := d.db.Preferences(context.Background())
		return err == nil && m["tui.editor.linenumbers"] == "true" && m["tui.editor.wrap"] == "true"
	})
}

// TestTheVimKeysCard: ? shows the Vim keys at the bottom right, the page keeping the keyboard;
// ? again hides it. About names the author and the license.
func TestTheVimKeysCard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.keys(t, key('?'))
	r.s.WaitFor(t, "the card", func(sc string) bool {
		return strings.Contains(sc, "Vim keys · Normal mode") && strings.Contains(sc, "dd yy  delete, copy the line")
	})
	rows := strings.Split(r.s.String(), "\n")
	for i, row := range rows {
		if strings.Contains(row, "n N      again, forward, back") && i != len(rows)-3 {
			t.Fatalf("the card's last line is at row %d of %d, want just over the status line's border:\n%s", i, len(rows), r.s)
		}
		if c := col(row, "╭ Vim keys"); c >= 0 && c < 20 {
			t.Fatalf("the card starts at column %d, want the right:\n%s", c, r.s)
		}
	}
	if !r.focused("editor") {
		t.Fatal("the card took the keyboard from the page")
	}
	r.keys(t, key('?'))
	r.s.WaitFor(t, "the card hidden", func(sc string) bool { return !strings.Contains(sc, "Vim keys") })
	if got := onLoop(r, r.h.aboutText); !strings.Contains(got, "By Yong Sung John Lee") || !strings.Contains(got, "Apache License, Version 2.0") {
		t.Errorf("About says %q", got)
	}
}

// TestThePageHoldsTheRulersColumnsWithTheLineNumbers: the ruler is the page's editable columns;
// the line numbers' gutter is added to the page, so a line as long as the ruler still fits.
func TestThePageHoldsTheRulersColumnsWithTheLineNumbers(t *testing.T) {
	line := strings.Repeat("y", 60)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", line + "z\n"}},
		daemonOpts{prefs: map[string]string{"tui.status.shown": "true", "tui.toast.seconds": "1", "tui.ruler": "60", "tui.editor.linenumbers": "true"}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 160, 20)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.s.WaitFor(t, "the note", func(string) bool { n := r.file(); return n.open && n.path == "a.md" })
	r.s.WaitForText(t, "   1  "+line)
	rows := strings.Split(r.s.String(), "\n")
	// 60 columns of text, a 6-column gutter and the border: 68 wide
	if l, rt := col(rows[0], "┌"), col(rows[0], "┐"); rt-l+1 != 68 {
		t.Fatalf("the page is %d wide, want 68 (60 of text, the gutter, the border):\n%s", rt-l+1, r.s)
	}
	// the 60 y's fill the text's columns; the 61st column's z wraps to the next row
	if !strings.Contains(rows[1], "   1  "+line+"│") || !strings.Contains(rows[2], "z") {
		t.Fatalf("the ruler's 60 columns are not the text's:\n%s", r.s)
	}
	// hidden, the page is the ruler and its border again
	r.h.p.Post(r.h.toggleLineNumbers)
	r.s.WaitFor(t, "narrower", func(sc string) bool {
		top := strings.Split(sc, "\n")[0]
		return col(top, "┐")-col(top, "┌")+1 == 62
	})
}

func TestClosingAFileGivesThePageItsDraftWidth(t *testing.T) {
	long := strings.Repeat("y\n", 10000) // five digits: a gutter one column wider
	d := startDaemonWith(t, "", map[string][]string{"kb": {"long.md", long}},
		daemonOpts{prefs: map[string]string{"tui.ruler": "60", "tui.editor.linenumbers": "true"}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 160, 20)
	r.ready(t)
	width := func(want int) func(string) bool {
		return func(sc string) bool {
			top := strings.Split(sc, "\n")[0]
			return col(top, "┐")-col(top, "┌")+1 == want
		}
	}
	r.h.p.Post(func() { r.h.openPath("long.md") })
	r.s.WaitFor(t, "the long note's page", width(69))
	// closed, the page is an empty draft's again
	r.h.p.Post(r.h.closeFile)
	r.s.WaitFor(t, "the draft's page", width(68))
}
