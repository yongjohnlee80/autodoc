//go:build gui

package tui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// fakeBrowser is a rasterizer that holds each drawing until it is let go, and says when one starts
// and when its context ends.
type fakeBrowser struct {
	mu        sync.Mutex
	started   chan context.Context
	release   chan struct{}
	calls     int
	width     int // the picture's width; 300 unless set
	height    int // its height; 200 unless set
	fail      error
	immediate bool
}

func newFakeBrowser() *fakeBrowser {
	return &fakeBrowser{started: make(chan context.Context, 64), release: make(chan struct{})}
}

func (f *fakeBrowser) rasterize(t *testing.T) func(context.Context, []byte, widget.Page) ([]byte, error) {
	return func(ctx context.Context, _ []byte, _ widget.Page) ([]byte, error) {
		f.mu.Lock()
		f.calls++
		w, h, fail, immediate := f.width, f.height, f.fail, f.immediate
		f.mu.Unlock()
		select { // told, while a test listens
		case f.started <- ctx:
		default:
		}
		if !immediate {
			select {
			case <-f.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if fail != nil {
			return nil, fail
		}
		return tallPage(t, max(w, 300), max(h, 200)), nil
	}
}

func (f *fakeBrowser) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

// paneDrawer is a mermaid.js Diagrammer on a running host whose browser is b.
func paneDrawer(t *testing.T, b *fakeBrowser) (*running, *mermaidJS) {
	t.Helper()
	r := runGUINote(t, fencedNote, func(h *Host) {
		h.browserOverride = func() bool { return true }
		h.rasterizeOverride = b.rasterize(t)
	})
	return r, onLoop(r, func() *mermaidJS { return newMermaidJS(r.h) })
}

// ask is a request for src at width, as the pane's page makes it, on the loop.
func ask(r *running, m *mermaidJS, ctx context.Context, src string, width float32, ready func()) guiwidget.DiagramAnswer {
	return onLoop(r, func() guiwidget.DiagramAnswer {
		return m.Diagram(ctx, guiwidget.DiagramRequest{Lang: "mermaid", Src: src, Width: width, Scale: 1}, ready)
	})
}

func startedIn(t *testing.T, b *fakeBrowser) context.Context {
	t.Helper()
	select {
	case ctx := <-b.started:
		return ctx
	case <-time.After(5 * time.Second):
		t.Fatal("no drawing started")
		return nil
	}
}

func endsIn(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}

// The last request leaving a drawing cancels it, in the browser: a closed pane, a new page, another
// engine leave no browser running for nobody. Its picture is not kept: the next request draws.
func TestADrawingNobodyWaitsForIsCancelled(t *testing.T) {
	b := newFakeBrowser()
	r, m := paneDrawer(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	ask(r, m, ctx, "graph TD\n  A --> B", 600, func() {})
	running := startedIn(t, b)
	cancel()
	if !endsIn(running, 2*time.Second) {
		t.Fatal("the drawing ran on after its only request left")
	}
	if a := ask(r, m, context.Background(), "graph TD\n  A --> B", 600, func() {}); a.State != guiwidget.Pending {
		t.Errorf("after a cancelled drawing: %v; want it drawn again", a.State)
	}
	startedIn(t, b)
}

// A drawing two requests wait for goes on when one leaves, and wakes the one still there.
func TestADrawingSharedGoesOnWhenOneRequestLeaves(t *testing.T) {
	b := newFakeBrowser()
	r, m := paneDrawer(t, b)
	gone, leave := context.WithCancel(context.Background())
	var mu sync.Mutex
	woke := map[string]int{}
	wake := func(who string) func() { return func() { mu.Lock(); woke[who]++; mu.Unlock() } }
	ask(r, m, gone, "graph TD\n  A --> B", 600, wake("gone"))
	ask(r, m, context.Background(), "graph TD\n  A --> B", 500, wake("stays"))
	running := startedIn(t, b)
	leave()
	if endsIn(running, 200*time.Millisecond) {
		t.Fatal("the drawing was cancelled while a request still waited")
	}
	close(b.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		stays, g := woke["stays"], woke["gone"]
		mu.Unlock()
		if stays == 1 {
			if g != 0 {
				t.Errorf("the request that left was woken %d times", g)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the request still waiting was never woken")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b.count() != 1 {
		t.Errorf("%d drawings for one diagram asked twice", b.count())
	}
}

// The program's end cancels every drawing.
func TestTheProgramsEndCancelsTheDrawings(t *testing.T) {
	b := newFakeBrowser()
	r, m := paneDrawer(t, b)
	parent, end := context.WithCancel(context.Background())
	m.parent = parent
	ask(r, m, context.Background(), "graph TD\n  A --> B", 600, func() {})
	running := startedIn(t, b)
	end()
	if !endsIn(running, 2*time.Second) {
		t.Fatal("a drawing outlived the program")
	}
}

// A width the pane changes to fits the picture drawn: no browser again. The pictures kept are
// bounded, by count and by their pixels, the newest kept; a failure is drawn again after a while.
func TestThePanesPicturesAreReusedAndBounded(t *testing.T) {
	b := newFakeBrowser()
	b.immediate = true
	r, m := paneDrawer(t, b)
	drawn := func(src string, width float32) guiwidget.DiagramAnswer {
		t.Helper()
		done := make(chan struct{}, 1)
		a := ask(r, m, context.Background(), src, width, func() { done <- struct{}{} })
		if a.State == guiwidget.Pending {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("%q was never drawn", src)
			}
			a = ask(r, m, context.Background(), src, width, func() {})
		}
		return a
	}
	if a := drawn("graph TD\n  A --> B", 600); a.State != guiwidget.Ready {
		t.Fatalf("drawn: %v", a.State)
	}
	if a := drawn("graph TD\n  A --> B", 200); a.State != guiwidget.Ready || a.Size.W > 200 || b.count() != 1 {
		t.Errorf("at another width: %v, %v wide, %d drawings; want the same picture fitted", a.State, a.Size.W, b.count())
	}

	for i := range maxPictures + 8 {
		drawn("graph TD\n  N"+string(rune('a'+i%26))+string(rune('a'+i/26))+" --> B", 600)
	}
	if n := onLoop(r, func() int { m.mu.Lock(); defer m.mu.Unlock(); return m.done.order.Len() }); n > maxPictures {
		t.Errorf("%d pictures kept; at most %d", n, maxPictures)
	}
	b.mu.Lock()
	b.width, b.height = 2048, 2048 // 16 MiB a picture: past maxPictureBytes long before maxPictures
	b.mu.Unlock()
	for i := range 8 {
		drawn("graph LR\n  W"+string(rune('a'+i%26))+string(rune('a'+i/26))+" --> B", 600)
	}
	if bytes := onLoop(r, func() int { m.mu.Lock(); defer m.mu.Unlock(); return m.done.bytes }); bytes > maxPictureBytes {
		t.Errorf("%d bytes of pictures kept; at most %d", bytes, maxPictureBytes)
	}

	b.mu.Lock()
	b.fail = errors.New("the browser crashed")
	b.mu.Unlock()
	clock := time.Now()
	m.now = func() time.Time { return clock }
	drawn("graph TD\n  F --> B", 600) // fails: the native answer
	before := b.count()
	drawn("graph TD\n  F --> B", 600)
	if b.count() != before {
		t.Error("a failure was drawn again at once; it is kept a while")
	}
	clock = clock.Add(failureFor)
	drawn("graph TD\n  F --> B", 600)
	if b.count() != before+1 {
		t.Error("a failure was never drawn again")
	}
}

// A request that ends while the drawing it waited for wakes the others is not woken: the first
// woken holds the rest back while both requests end.
func TestARequestEndedWhileTheOthersAreWokenIsNotWoken(t *testing.T) {
	b := newFakeBrowser()
	r, m := paneDrawer(t, b)
	ctxs := map[string]context.CancelFunc{}
	inFirst, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var woken []string
	wake := func(who string) func() {
		return func() {
			mu.Lock()
			woken = append(woken, who)
			first := len(woken) == 1
			mu.Unlock()
			if first {
				close(inFirst)
				<-release
			}
		}
	}
	for _, who := range []string{"one", "two"} {
		ctx, cancel := context.WithCancel(context.Background())
		ctxs[who] = cancel
		ask(r, m, ctx, "graph TD\n  A --> B", 600, wake(who))
	}
	startedIn(t, b)
	close(b.release)
	select {
	case <-inFirst:
	case <-time.After(5 * time.Second):
		t.Fatal("no request was woken")
	}
	for _, cancel := range ctxs {
		cancel()
	}
	close(release)
	time.Sleep(100 * time.Millisecond) // the drawing's goroutine wakes the rest, or not
	mu.Lock()
	defer mu.Unlock()
	if len(woken) != 1 {
		t.Errorf("woken %v; the second ended before its turn", woken)
	}
}
