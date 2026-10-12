package tui

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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
	w := &watching{fake: fake{name: "w"}}
	onLoop(r, func() bool { r.h.reg.features = append(r.h.reg.features, w); return true })
	before := onLoop(r, func() FileList { return core{r.h}.Files() })
	onLoop(r, func() bool { r.h.listFiles(); return true })
	r.s.WaitFor(t, "the listing heard", func(string) bool { w.mu.Lock(); defer w.mu.Unlock(); return w.listed > 0 })
	after := onLoop(r, func() FileList { return core{r.h}.Files() })
	if !after.Loaded || after.Gen <= before.Gen || !slices.Contains(after.Paths, "guide.md") {
		t.Errorf("Files after a listing: %+v (before %+v)", after, before)
	}
	if ws := onLoop(r, func() []string { return core{r.h}.Workspaces() }); !slices.Equal(ws, []string{"kb"}) {
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
