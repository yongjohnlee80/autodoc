package tui

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// watching is a feature that records the page's lifecycle and the listings it hears.
type watching struct {
	fake
	mu     sync.Mutex
	events []string
	edited []Page
	listed int
}

func (w *watching) log(e string) { w.mu.Lock(); w.events = append(w.events, e); w.mu.Unlock() }
func (w *watching) Shown(p Page) { w.log("shown " + p.Ref.path) }
func (w *watching) Edited(p Page) {
	w.mu.Lock()
	w.edited = append(w.edited, p)
	w.mu.Unlock()
	w.log("edited")
}
func (w *watching) Moved(line, at int) { w.log(fmt.Sprintf("moved %d %d", line, at)) }
func (w *watching) Reread(p Page)      { w.log("reread " + p.Ref.path) }
func (w *watching) Closed()            { w.log("closed") }
func (w *watching) Listed()            { w.mu.Lock(); w.listed++; w.mu.Unlock() }
func (w *watching) seen() []string     { w.mu.Lock(); defer w.mu.Unlock(); return slices.Clone(w.events) }

// The registry hands a watcher the page in order: nothing before a page is shown; Closed before
// each later Shown; Edited, Moved and Reread only while a page is shown.
func TestTheRegistryHandsThePageInOrder(t *testing.T) {
	w := &watching{fake: fake{name: "w"}}
	r, err := newRegistry(w)
	if err != nil {
		t.Fatal(err)
	}
	a, b := Page{Ref: fileRef{"kb", "a.md"}}, Page{Ref: fileRef{"kb", "b.md"}}
	r.edited(a)
	r.moved(1, 2)
	r.reread(a) // no page shown yet: none of these reaches it
	r.shown(a)
	r.edited(a)
	r.moved(3, 14)
	r.reread(a)
	r.shown(b)
	want := []string{"shown a.md", "edited", "moved 3 14", "reread a.md", "closed", "shown b.md"}
	if got := w.seen(); !slices.Equal(got, want) {
		t.Errorf("events %q, want %q", got, want)
	}
}

// One typing pause is one snapshot of the text, handed as the same bytes to every watcher and kept
// as the host's page; opening a file shows it, and moving the cursor is heard without an edit.
func TestOnePauseIsOneSnapshotForEveryWatcher(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	one, two := &watching{fake: fake{name: "one"}}, &watching{fake: fake{name: "two"}}
	onLoop(r, func() bool { r.h.reg.features = append(r.h.reg.features, one, two); return true })
	r.h.p.Post(func() { r.h.openPath("guide.md") })
	r.waitFile(t, "guide.md")
	r.s.WaitFor(t, "guide.md shown to the watchers", func(string) bool {
		return slices.Contains(one.seen(), "shown guide.md") && slices.Contains(two.seen(), "shown guide.md")
	})
	r.h.p.Post(func() {
		r.h.editor.SetValue("# Typed\n\nnew\n")
		r.h.edited()
	})
	r.s.WaitFor(t, "the pause heard by both", func(string) bool {
		one.mu.Lock()
		defer one.mu.Unlock()
		two.mu.Lock()
		defer two.mu.Unlock()
		return len(one.edited) == 1 && len(two.edited) == 1
	})
	page := onLoop(r, func() Page { return r.h.page })
	if string(one.edited[0].Text) != "# Typed\n\nnew\n" || &one.edited[0].Text[0] != &two.edited[0].Text[0] ||
		&page.Text[0] != &one.edited[0].Text[0] {
		t.Errorf("not one snapshot: %q, shared with the second watcher %v, with the host's page %v",
			one.edited[0].Text, &one.edited[0].Text[0] == &two.edited[0].Text[0], &page.Text[0] == &one.edited[0].Text[0])
	}
	r.h.p.Post(func() { r.h.core.SetLine(2, 0) })
	r.s.WaitFor(t, "the cursor's move heard", func(string) bool {
		return slices.ContainsFunc(one.seen(), func(e string) bool { return strings.HasPrefix(e, "moved 2 ") })
	})
}

// A pause hands the frontmatter check the rest of its quiet period: its wait after the pause plus
// the pause is validateDelay, timed from the last keystroke as before the one debounce, not
// validateDelay after the pause.
func TestTheFrontmatterCheckWaitsFromTheLastKeystroke(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.openPath("guide.md") })
	r.waitFile(t, "guide.md")
	got := onLoop(r, func() []time.Duration {
		var waits []time.Duration
		scheduled = func(d time.Duration) { waits = append(waits, d) }
		defer func() { scheduled = nil }()
		r.h.pageSoon()
		r.h.pagePaused()
		return waits
	})
	if len(got) < 2 || got[0] != editPause || !slices.Contains(got[1:], validateDelay-editPause) {
		t.Fatalf("waits scheduled %v: want the pause (%v), then the check's rest (%v)", got, editPause, validateDelay-editPause)
	}
}

// SetPref applies a feature's preference to the live prefs at once, through its row, and stores
// it; Files and Workspaces are the host's snapshots, and a listing is heard by a Lister.
func TestCoreSetPrefFilesAndListings(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	if wrap := onLoop(r, func() bool { core{r.h}.SetPref(prefWrap, "false"); return core{r.h}.Prefs().wrap }); wrap {
		t.Error("SetPref did not reach the live prefs")
	}
	// the chooser follows: the Settings dialog's wrap row is "false", the second of its choices
	if idx := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.wrapIndex"); return fmt.Sprint(v) }); !strings.HasPrefix(idx, "{number 1 ") {
		t.Errorf("App.wrapIndex after SetPref(wrap, false) = %s; want the row of false (1)", idx)
	}
	w := &watching{fake: fake{name: "w"}}
	onLoop(r, func() bool { r.h.reg.features = append(r.h.reg.features, w); return true })
	before := onLoop(r, func() FileList { return core{r.h}.Files() })
	onLoop(r, func() bool { r.h.listFiles(); return true })
	r.s.WaitFor(t, "the listing heard", func(string) bool { w.mu.Lock(); defer w.mu.Unlock(); return w.listed > 0 })
	after := onLoop(r, func() FileList { return core{r.h}.Files() })
	if !after.Loaded || after.Gen <= before.Gen || !slices.Contains(after.Paths, "guide.md") {
		t.Errorf("Files after a listing: %+v (before %+v)", after, before)
	}
	if ws := onLoop(r, func() []Workspace { return core{r.h}.Workspaces() }); len(ws) != 1 || ws[0].Name != "kb" || ws[0].State != "ready" || ws[0].Root == "" {
		t.Errorf("Workspaces = %v", ws)
	}
	onLoop(r, func() bool { r.h.resetFiles(); return true })
	if reset := onLoop(r, func() FileList { return core{r.h}.Files() }); reset.Loaded || reset.Paths != nil || reset.Gen <= after.Gen {
		t.Errorf("Files after a reset: %+v", reset)
	}
}

// The notifications' history is a feature of its own: a notification is kept, the dialog lists it,
// and clearing empties it.
func TestTheNoticesHistoryKeepsAndClears(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	onLoop(r, func() bool { r.h.notify("kestrel landed"); r.h.noticeLog.open(); return true })
	r.s.WaitForText(t, "kestrel landed")
	if n := onLoop(r, func() int { return r.h.noticeLog.rows.Len() }); n == 0 {
		t.Fatal("the history lists nothing")
	}
	onLoop(r, func() bool { r.h.noticeLog.clear(); return true })
	if n := onLoop(r, func() int { return r.h.noticeLog.rows.Len() }); n != 0 {
		t.Errorf("after clearing, %d kept", n)
	}
}

// SetPref goes through the row alone: the live prefs read the value at once, the row's State is
// published, and its Changed runs when the value changed, not when it is set again.
func TestSetPrefGoesThroughTheRowAlone(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	changed, stated := 0, ""
	row := ValuePref("test.feature.mode", "a", nil)
	row.State = func(p prefs) map[string]any { stated = p.Value("test.feature.mode"); return map[string]any{} }
	row.Changed = func(prefs) { changed++ }
	onLoop(r, func() bool {
		r.h.prefRows = append(r.h.prefRows, row)
		core{r.h}.SetPref("test.feature.mode", "b")
		core{r.h}.SetPref("test.feature.mode", "b") // the same again: nothing changed
		return true
	})
	got := onLoop(r, func() [2]any { return [2]any{core{r.h}.Prefs().Value("test.feature.mode"), changed} })
	if got != [2]any{"b", 1} {
		t.Errorf("value %v, Changed ran %v times; want b, once", got[0], got[1])
	}
	if v := onLoop(r, func() string { return stated }); v != "b" {
		t.Errorf("the row's State was published with %q; want b", v)
	}
}

// A keystroke after a pause drops the frontmatter check that pause scheduled: the check of the
// older text never runs, and the next pause checks the new text.
func TestAKeystrokeAfterThePauseDropsItsCheck(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	sess := NewSession(d.sock, nil)
	var mu sync.Mutex
	var checked []string
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "doc.validate" && len(params) == 3 {
			if b, ok := params[2].([]byte); ok {
				mu.Lock()
				checked = append(checked, string(b))
				mu.Unlock()
			}
		}
	})
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.openPath("guide.md") })
	r.waitFile(t, "guide.md")
	r.h.p.Post(func() {
		r.h.editor.SetValue("# Older\n")
		r.h.pagePaused() // its check is due validateDelay-editPause from now
	})
	// a keystroke after the pause, before its check: the next pause would come after that check
	time.Sleep((validateDelay - editPause) / 3)
	r.h.p.Post(func() {
		r.h.editor.SetValue("# Newer\n")
		r.h.edited()
	})
	r.s.WaitFor(t, "the newer text checked", func(string) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(checked, "# Newer\n")
	})
	mu.Lock()
	defer mu.Unlock()
	if slices.Contains(checked, "# Older\n") {
		t.Errorf("the older text's check ran after a keystroke changed it: %q", checked)
	}
}

// Showing another page drops a typing pause still to come: the page shown is not told it was
// edited by typing that was the page before's.
func TestShowingAPageDropsThePendingPause(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	w := &watching{fake: fake{name: "w"}}
	onLoop(r, func() bool { r.h.reg.features = append(r.h.reg.features, w); return true })
	r.h.p.Post(func() { r.h.openPath("guide.md") })
	r.waitFile(t, "guide.md")
	r.s.WaitFor(t, "guide.md shown", func(string) bool { return slices.Contains(w.seen(), "shown guide.md") })
	onLoop(r, func() bool {
		r.h.editor.SetValue("typed\n")
		r.h.edited() // its pause is to come
		r.h.closeFile()
		return true
	})
	time.Sleep(3 * editPause) // the dropped pause would have come by now
	if got := w.seen(); slices.Contains(got, "edited") {
		t.Errorf("the draft was told it was edited by the file before's typing: %q", got)
	}
}

// A file opened raw (an HTML file's own bytes) is a page read as plain text; a derived document's
// text and the draft read as Markdown.
func TestAPagesKindFollowsRawAndDerived(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "guide.md", guideFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	got := onLoop(r, func() [3]kind.Kind {
		saved := r.h.file
		defer func() { r.h.file = saved }()
		r.h.file = openedFile{ws: "kb", path: "page.html", open: true, raw: true}
		raw := r.h.pageKind()
		r.h.file = openedFile{ws: "kb", path: "manual.pdf", open: true, derived: "PDF"}
		derived := r.h.pageKind()
		r.h.file = openedFile{}
		return [3]kind.Kind{raw, derived, r.h.pageKind()}
	})
	if got != [3]kind.Kind{kind.Text, kind.Markdown, kind.Markdown} {
		t.Errorf("kinds: raw %v, derived %v, draft %v; want text, Markdown, Markdown", got[0], got[1], got[2])
	}
}

// The features moved out of legacy are registered as themselves: their commands and sources are
// theirs, not the host's (B5). A merge that brings a host-owned copy back fails here.
func TestTheMovedFeaturesAreRegistered(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	byName := map[string]Feature{}
	for _, f := range h.features() {
		byName[f.Name()] = f
	}
	for name, cmd := range map[string]string{"notices": "App.openNotices", "recent": "App.openRecent"} {
		f, ok := byName[name]
		if !ok {
			t.Errorf("no %s feature registered", name)
			continue
		}
		if _, ok := f.Commands()[cmd]; !ok {
			t.Errorf("%s is not the %s feature's command", cmd, name)
		}
		if _, ok := byName["legacy"].Commands()[cmd]; ok {
			t.Errorf("%s is the host's (legacy) again", cmd)
		}
	}
}
