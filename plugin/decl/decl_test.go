package decl

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/golib/decl"
	"github.com/yongjohnlee80/golib/parse/qml"
	"github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/controls"
)

// fakeHost records the frames and closes a Card sends, in one ordered log with whatever else a
// test notes there.
type fakeHost struct {
	mu     sync.Mutex
	log    []string
	frames []*plugin.Frame
	closes int
}

func (h *fakeHost) Frame(f *plugin.Frame) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.frames = append(h.frames, f)
	h.log = append(h.log, "frame")
	return nil
}

func (h *fakeHost) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closes++
	h.log = append(h.log, "close")
	return nil
}

func (h *fakeHost) note(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = append(h.log, s)
}

func (h *fakeHost) snapshot() (log []string, frames []*plugin.Frame, closes int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.log...), append([]*plugin.Frame(nil), h.frames...), h.closes
}

// text is a frame's rows run together, one per line.
func text(f *plugin.Frame) string {
	var b strings.Builder
	for _, r := range f.Rows() {
		for _, run := range r {
			b.WriteString(run.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// waitFor polls cond until it holds; the program runs on its own goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// lastHas waits for the latest frame to hold s.
func lastHas(t *testing.T, h *fakeHost, s string) *plugin.Frame {
	t.Helper()
	var last *plugin.Frame
	waitFor(t, "a frame holding "+s, func() bool {
		_, frames, _ := h.snapshot()
		if len(frames) == 0 {
			return false
		}
		last = frames[len(frames)-1]
		return strings.Contains(text(last), s)
	})
	return last
}

var themeA = plugin.Theme{Name: "a", Colors: map[string]string{
	"app.window": "#112233", "app.windowText": "#ddeeff", "app.base": "#000000", "app.text": "#ffffff",
}}

const palette = "import tui 1.0\nimport autodoc.plugin 1.0\nimport hello 1.0\n" +
	"Window { palette.window: Theme.app.window; palette.windowText: Theme.app.windowText\n"

// start opens a Card over opts on a fake host, w×h, and closes it when the test ends.
func start(t *testing.T, o Options, w, h int) (*Card, *fakeHost) {
	t.Helper()
	c := New(o)
	host := &fakeHost{}
	c.open(host, nil, plugin.Open{Protocol: plugin.Protocol, Width: w, Height: h, Theme: themeA})
	t.Cleanup(c.Close)
	return c, host
}

func TestSetupRunsBeforeTheFirstFrame(t *testing.T) {
	var c *Card
	var host *fakeHost
	o := Options{
		Modal: true,
		Program: []tuidecl.ProgramOption{
			tuidecl.LayoutSource("card.qml", []byte(palette+" Text { text: App.status } }")),
			tuidecl.Singleton("hello", "1.0", "App"),
			tuidecl.Sources(map[string]any{"App.status": "starting"}),
		},
		Setup: func(p *tuidecl.Program, _ *plugin.Peer) error {
			host.note("setup")
			return p.Set("App.status", "ready")
		},
	}
	c, host = New(o), &fakeHost{}
	c.open(host, nil, plugin.Open{Width: 20, Height: 3, Theme: themeA})
	defer c.Close()
	waitFor(t, "the first frame", func() bool { _, f, _ := host.snapshot(); return len(f) > 0 })
	log, frames, _ := host.snapshot()
	if log[0] != "setup" || log[1] != "frame" {
		t.Fatalf("order = %q, want setup, then the first frame", log)
	}
	if got := text(frames[0]); !strings.Contains(got, "ready") || strings.Contains(got, "starting") {
		t.Fatalf("the first frame = %q, want Setup's state in it", got)
	}
}

func TestWithoutSetupTheProgramStillDraws(t *testing.T) {
	_, host := start(t, Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(palette+" Text { text: \"plain\" } }")),
		tuidecl.Singleton("hello", "1.0", "App"),
	}}, 20, 3)
	lastHas(t, host, "plain")
}

func TestKeysReachTheProgramAndATypedFieldSurvivesATheme(t *testing.T) {
	var mu sync.Mutex
	var got []string
	c, host := start(t, Options{Modal: true, Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(palette+
			" Flex { direction: Tui.Vertical\n Text { text: \"name:\" }\n TextField { id: user; onAccepted: App.login(user.text) } } }")),
		tuidecl.Singleton("hello", "1.0", "App"),
		tuidecl.Types(controls.Types()...),
		tuidecl.Handlers(map[string]decl.HandlerFunc{"App.login": func(args []qml.SpecValue) error {
			mu.Lock()
			got = append(got, args[0].Raw)
			mu.Unlock()
			return nil
		}}),
	}}, 30, 4)
	lastHas(t, host, "name:")
	c.Key(plugin.Key{Key: "Tab"})
	for _, r := range "ann" {
		c.Key(plugin.Key{Key: string(r), Text: string(r)})
	}
	lastHas(t, host, "ann")
	c.Theme(plugin.Theme{Name: "b", Colors: map[string]string{"app.window": "#445566", "app.windowText": "#000000"}})
	f := lastHas(t, host, "ann")
	waitFor(t, "the new window colour", func() bool {
		_, frames, _ := host.snapshot()
		f = frames[len(frames)-1]
		for _, run := range f.Rows()[0] {
			if strings.Contains(run.Text, "name:") {
				return run.BG == "#445566"
			}
		}
		return false
	})
	if !strings.Contains(text(f), "ann") {
		t.Fatalf("the theme change lost the typed text:\n%s", text(f))
	}
	c.Key(plugin.Key{Key: "Enter"})
	waitFor(t, "the sign-in", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	if got[0] != "ann" {
		t.Fatalf("signed in as %q", got[0])
	}
}

func TestPostReachesTheLoopAndResizeReachesTheFrame(t *testing.T) {
	c, host := start(t, Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(palette+" Text { text: App.status } }")),
		tuidecl.Singleton("hello", "1.0", "App"),
		tuidecl.Sources(map[string]any{"App.status": "one"}),
	}}, 20, 3)
	lastHas(t, host, "one")
	if !c.Post(func() { _ = c.Program().Set("App.status", "two") }) {
		t.Fatal("Post refused while running")
	}
	lastHas(t, host, "two")
	c.Resize(9, 2)
	waitFor(t, "a 9×2 frame", func() bool {
		_, frames, _ := host.snapshot()
		w, h := frames[len(frames)-1].Size()
		return w == 9 && h == 2
	})
}

func TestAProgramThatCannotBeBuiltSaysSoAndCloses(t *testing.T) {
	c, host := start(t, Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte("import tui 1.0\nNoSuchType { }")),
	}}, 40, 3)
	log, frames, closes := host.snapshot()
	if len(frames) != 1 || !strings.Contains(text(frames[0]), "could not start") || closes != 1 {
		t.Fatalf("log %q: want one frame naming the failure, then a close", log)
	}
	if c.Post(func() {}) {
		t.Fatal("Post accepted after a failed Open")
	}
	c.Key(plugin.Key{Key: "a", Text: "a"}) // nothing to send it to: dropped
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close after a failed Open did not return")
	}
}

func TestAFailingSetupDestroysTheTreeAndCloses(t *testing.T) {
	c, host := start(t, Options{
		Program: []tuidecl.ProgramOption{
			tuidecl.LayoutSource("card.qml", []byte(palette+" Text { text: \"x\" } }")),
			tuidecl.Singleton("hello", "1.0", "App"),
		},
		Setup: func(*tuidecl.Program, *plugin.Peer) error { return context.DeadlineExceeded },
	}, 40, 3)
	_, frames, closes := host.snapshot()
	if len(frames) != 1 || !strings.Contains(text(frames[0]), "deadline") || closes != 1 {
		t.Fatalf("frames %d closes %d: want the failure named once, then a close", len(frames), closes)
	}
	if c.Program() != nil || c.state() != failed {
		t.Fatal("a failed Setup left a program running")
	}
}

func TestCloseBeforeRunHasStarted(t *testing.T) {
	c := New(Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(palette+" Text { text: \"x\" } }")),
		tuidecl.Singleton("hello", "1.0", "App"),
	}})
	c.open(&fakeHost{}, nil, plugin.Open{Width: 10, Height: 2, Theme: themeA})
	began := time.Now()
	c.Close() // at once: Run may not have stored its own cancel yet
	if d := time.Since(began); d >= closeWait {
		t.Fatalf("Close took %s: it waited out its bound instead of the program", d)
	}
	if c.state() != stopped {
		t.Fatalf("state %d after Close, want stopped", c.state())
	}
}

func TestAProgramThatQuitsClosesTheCard(t *testing.T) {
	var c *Card
	c, host := start(t, Options{Modal: true, Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(palette+" Text { text: \"bye\" }\n Shortcut { sequence: \"x\"; onActivated: App.quit() } }")),
		tuidecl.Singleton("hello", "1.0", "App"),
		tuidecl.Commands(map[string]func() error{"App.quit": func() error { c.Program().Quit(); return nil }}),
	}}, 20, 3)
	lastHas(t, host, "bye")
	c.Key(plugin.Key{Key: "x", Text: "x"})
	waitFor(t, "the card closed", func() bool { _, _, closes := host.snapshot(); return closes == 1 })
	waitFor(t, "stopped", func() bool { return c.state() == stopped })
	if c.Post(func() {}) {
		t.Fatal("Post accepted after the program stopped")
	}
	for range 2*eventQueue + 10 { // more than the queue holds, with no loop to drain it: dropped, never blocked
		c.Key(plugin.Key{Key: "y", Text: "y"})
	}
}

func TestAPostQueuedBeforeCloseNeverRunsAfterIt(t *testing.T) {
	c, host := start(t, Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(palette+" Text { text: \"x\" } }")),
		tuidecl.Singleton("hello", "1.0", "App"),
	}}, 10, 2)
	lastHas(t, host, "x")
	hold, held := make(chan struct{}), make(chan struct{})
	c.Post(func() { close(held); <-hold }) // the loop is busy here
	<-held
	var ran sync.Mutex
	after := false
	c.Post(func() { ran.Lock(); after = true; ran.Unlock() })
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	waitFor(t, "Close to cancel", func() bool { return c.ctx.Err() != nil })
	close(hold)
	<-closed
	ran.Lock()
	defer ran.Unlock()
	if after {
		t.Fatal("a Post queued before Close ran after it")
	}
}

// ---------------------------------------------------------------- the backend, directly

func newTestBackend(w, h int) (*frameBackend, *fakeHost, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	host := &fakeHost{}
	return newFrameBackend(host, w, h, ctx, map[string]string{"app.text": "#ffffff", "app.base": "#000000"}), host, cancel
}

// drained receives from Events until it is closed, failing after the bound: a closed buffered
// channel can still hand over what was queued before it closed.
func drained(t *testing.T, b *frameBackend) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-b.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("Events was not closed after Stop returned")
		}
	}
}

func TestStopKeepsTheBackendContract(t *testing.T) {
	t.Run("repeated and concurrent", func(t *testing.T) {
		b, _, cancel := newTestBackend(4, 1)
		defer cancel()
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() { defer wg.Done(); _ = b.Stop() }()
		}
		wg.Wait()
		_ = b.Stop()
		drained(t, b)
	})
	t.Run("a post blocked on a full queue is released", func(t *testing.T) {
		b, _, cancel := newTestBackend(4, 1)
		defer cancel()
		for range eventQueue {
			b.post(tui.ResizeEvent{W: 1, H: 1})
		}
		blocked := make(chan struct{})
		go func() { b.post(tui.ResizeEvent{W: 2, H: 2}); close(blocked) }()
		select {
		case <-blocked:
			t.Fatal("a post on a full queue did not block")
		case <-time.After(20 * time.Millisecond):
		}
		_ = b.Stop()
		select {
		case <-blocked:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop did not release the blocked post")
		}
		drained(t, b)
	})
	t.Run("posts racing Stop, and after it", func(t *testing.T) {
		b, _, cancel := newTestBackend(4, 1)
		defer cancel()
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 200 {
					b.post(tui.ResizeEvent{W: 1, H: 1})
				}
			}()
		}
		_ = b.Stop()
		wg.Wait()
		b.post(tui.ResizeEvent{W: 1, H: 1}) // after Stop: dropped, no panic
		drained(t, b)
	})
}

func full(w, h int, s string) []tui.CellUpdate {
	var d []tui.CellUpdate
	for y := range h {
		for x := range w {
			d = append(d, tui.CellUpdate{X: x, Y: y, Cell: tui.Cell{Content: s, Width: 1}})
		}
	}
	return d
}

func TestEachFrameIsSentAtTheSizeItWasDrawnAt(t *testing.T) {
	b, host, cancel := newTestBackend(80, 24)
	defer cancel()
	_ = b.FlushSized(tui.Size{W: 80, H: 24}, full(80, 24, "a"))
	b.setWant(20, 1) // the host shrank the card; the App has not drawn at 20×1 yet
	_ = b.FlushSized(tui.Size{W: 80, H: 24}, []tui.CellUpdate{{X: 79, Y: 23, Cell: tui.Cell{Content: "z", Width: 1}}})
	_ = b.FlushSized(tui.Size{W: 20, H: 1}, full(20, 1, "n"))
	_, frames, _ := host.snapshot()
	sizes := [][2]int{}
	for _, f := range frames {
		w, h := f.Size()
		sizes = append(sizes, [2]int{w, h})
	}
	if len(sizes) != 3 || sizes[1] != [2]int{80, 24} || sizes[2] != [2]int{20, 1} {
		t.Fatalf("sizes %v: the old frame at 80×24, then the new at 20×1", sizes)
	}
	if got := strings.TrimRight(text(frames[2]), "\n"); got != strings.Repeat("n", 20) {
		t.Fatalf("the 20×1 frame = %q", got)
	}
	// a resize there and back before the App drew keeps the grid: a partial diff applies to it
	b.setWant(5, 5)
	b.setWant(20, 1)
	_ = b.FlushSized(tui.Size{W: 20, H: 1}, []tui.CellUpdate{{X: 0, Y: 0, Cell: tui.Cell{Content: "m", Width: 1}}})
	_, frames, _ = host.snapshot()
	if got := strings.TrimRight(text(frames[3]), "\n"); got != "m"+strings.Repeat("n", 19) {
		t.Fatalf("after a same-size round trip = %q, want the grid kept", got)
	}
	// a zero size is an empty frame
	_ = b.FlushSized(tui.Size{}, nil)
	_, frames, _ = host.snapshot()
	if w, h := frames[4].Size(); w != 0 || h != 0 {
		t.Fatalf("a zero size sent %d×%d", w, h)
	}
}

func TestCellsBecomeTheFramesStyles(t *testing.T) {
	for _, c := range []struct {
		in   tui.CellColor
		want string
	}{
		{tui.CellColor{}, ""},
		{tui.CellColor{Kind: tui.CellColorANSI, Index: 9}, "brightred"},
		{tui.CellColor{Kind: tui.CellColorANSI256, Index: 4}, "blue"},
		{tui.CellColor{Kind: tui.CellColorANSI256, Index: 196}, "#ff0000"},
		{tui.CellColor{Kind: tui.CellColorANSI256, Index: 244}, "#808080"},
		{tui.CellColor{Kind: tui.CellColorRGB, R: 1, G: 2, B: 3}, "#010203"},
	} {
		if got := colour(c.in); got != c.want {
			t.Errorf("colour(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
	colors := map[string]string{"app.text": "#ffffff", "app.base": "#000000"}
	rev := tui.CellAttrs{Mask: tui.AttrReverse | tui.AttrBold}
	if st := cellStyle(rev, false, colors); st.FG != "#000000" || st.BG != "#ffffff" || !st.Bold {
		t.Errorf("reverse of the defaults = %+v, want the panel's colours swapped", st)
	}
	if st := cellStyle(rev, true, colors); st.FG != "" || st.BG != "" {
		t.Errorf("reverse under the cursor = %+v, want them cancelled", st)
	}
	if st := cellStyle(tui.CellAttrs{FG: tui.CellColor{Kind: tui.CellColorRGB, R: 255}}, true, colors); st.FG != "#000000" || st.BG != "#ff0000" {
		t.Errorf("the cursor on red text = %+v", st)
	}

	b, host, cancel := newTestBackend(4, 1)
	defer cancel()
	b.ShowCursor()
	b.SetCursor(2, 0) // the continuation of the wide cell at 1: the cursor marks its head
	_ = b.FlushSized(tui.Size{W: 4, H: 1}, []tui.CellUpdate{
		{X: 0, Y: 0, Cell: tui.Cell{Content: "a", Width: 1}},
		{X: 1, Y: 0, Cell: tui.Cell{Content: "表", Width: 2}},
		{X: 3, Y: 0, Cell: tui.Cell{Content: "b", Width: 1}},
	})
	_, frames, _ := host.snapshot()
	row := frames[0].Rows()[0]
	var head plugin.Run
	for _, r := range row {
		if strings.Contains(r.Text, "表") {
			head = r
		}
	}
	if head.BG != "#ffffff" || strings.TrimRight(text(frames[0]), "\n") != "a表b" {
		t.Fatalf("row %+v: the wide cell marked by the cursor, and nothing shifted", row)
	}
	if b.grid[1].Attrs.Mask&tui.AttrReverse != 0 || b.grid[1].Attrs.BG.Kind != tui.CellColorDefault {
		t.Fatal("drawing the cursor changed the grid")
	}
	// narrow over the wide head: the continuation goes, b stays
	_ = b.FlushSized(tui.Size{W: 4, H: 1}, []tui.CellUpdate{
		{X: 1, Y: 0, Cell: tui.Cell{Content: "x", Width: 1}},
		{X: 2, Y: 0, Cell: tui.Cell{Content: "y", Width: 1}},
	})
	_, frames, _ = host.snapshot()
	if got := strings.TrimRight(text(frames[1]), "\n"); got != "axyb" {
		t.Fatalf("narrow over wide = %q", got)
	}
}

func TestKeysAndThePointerAsTheTerminalSendsThem(t *testing.T) {
	for _, name := range []string{"Up", "Enter", "Tab", "F5", "PageDown", "Backspace"} {
		code, _ := plugin.KeyCode(name)
		if e := keyEvent(plugin.Key{Key: name}); e.Code != code || e.Text != "" || e.Kind != tui.KeyPress {
			t.Errorf("%s = %+v, want code %v", name, e, code)
		}
	}
	if e := keyEvent(plugin.Key{Key: "한", Text: "한글"}); e.Code != '한' || e.Text != "한글" {
		t.Errorf("an IME commit = %+v", e)
	}
	if e := keyEvent(plugin.Key{Key: "a", Ctrl: true}); e.Code != 'a' || e.Text != "" || e.Mods != tui.ModCtrl {
		t.Errorf("Ctrl+a = %+v", e)
	}
	if e := keyEvent(plugin.Key{Key: "Tab", Shift: true}); e.Mods != tui.ModShift {
		t.Errorf("Shift+Tab = %+v", e)
	}
	press := mouseEvents(plugin.Pointer{Kind: "press", Button: "left", X: 3, Y: 1, Count: 1})
	if len(press) != 2 || press[0].(tui.MouseEvent).Kind != tui.MousePress || press[1].(tui.MouseEvent).Kind != tui.MouseRelease {
		t.Errorf("a press = %+v, want a press and its release", press)
	}
	if wheel := mouseEvents(plugin.Pointer{Kind: "wheel", Button: "up", Count: 3}); len(wheel) != 3 || wheel[0].(tui.MouseEvent).Button != tui.WheelUp {
		t.Errorf("a wheel of 3 = %+v", wheel)
	}
	if mouseEvents(plugin.Pointer{Kind: "press", Button: "right", Count: 1}) != nil {
		t.Error("a right press was sent: AutoDoc keeps it for the card's menu")
	}
}

// TestAQueuedFnChecksTheCardWhenItRuns: the guard Post wraps a fn in runs it while the Card is live
// and skips it once Close has cancelled, whenever it was queued. (The integration cell above shows
// the same through a running program, where the loop may end before reaching the fn at all.)
func TestAQueuedFnChecksTheCardWhenItRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ran := 0
	before, after := whileLive(ctx, func() { ran++ }), whileLive(ctx, func() { ran += 10 })
	before()
	cancel()
	after()
	if ran != 1 {
		t.Fatalf("ran = %d: want the fn run while live, and the one run after cancel skipped", ran)
	}
}
