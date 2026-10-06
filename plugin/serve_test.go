package plugin

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// host is a test's end of the link: what the TUI would be.
type host struct {
	link  *Link
	got   chan note
	serve chan error
}

// start runs h under ServeConn over two os.Pipe pairs, as the TUI runs a plugin, and returns the
// host's end.
func start(t *testing.T, h Handler) *host {
	t.Helper()
	toPlugin, fromHost, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fromPlugin, toHost, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hs := &host{got: make(chan note, 64), serve: make(chan error, 1)}
	go func() { hs.serve <- ServeConn(ctx, FileConn(toPlugin, toHost), h) }()
	hs.link, err = NewLink(ctx, FileConn(fromPlugin, fromHost), func(m string, p []any) { hs.got <- note{m, p} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hs.link.Close() })
	return hs
}

func (hs *host) send(t *testing.T, method string, params []any) {
	t.Helper()
	if err := hs.link.Notify(context.Background(), method, params); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func (hs *host) next(t *testing.T) note {
	t.Helper()
	select {
	case n := <-hs.got:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("the plugin sent nothing")
	}
	return note{}
}

func (hs *host) served(t *testing.T) error {
	t.Helper()
	select {
	case err := <-hs.serve:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("ServeConn did not return")
	}
	return nil
}

// recorder is a Handler that writes down each call, in order.
type recorder struct {
	mu     sync.Mutex
	calls  []string
	onOpen func(p *Peer)
	keyLag time.Duration
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *recorder) Open(p *Peer, o Open) {
	r.add(fmt.Sprintf("open %dx%d %s %s", o.Width, o.Height, o.Theme.Name, o.Theme.Colors["app.window"]))
	if r.onOpen != nil {
		r.onOpen(p)
	}
}
func (r *recorder) Key(k Key) {
	time.Sleep(r.keyLag)
	r.add(fmt.Sprintf("key %s %q ctrl=%v", k.Key, k.Text, k.Ctrl))
}
func (r *recorder) Resize(w, h int) { r.add(fmt.Sprintf("resize %dx%d", w, h)) }
func (r *recorder) Theme(t Theme)   { r.add("theme " + t.Name) }
func (r *recorder) Close()          { r.add("close") }

// hider is a recorder that is told of hiding and showing.
type hider struct{ recorder }

func (h *hider) Hide() { h.add("hide") }
func (h *hider) Show() { h.add("show") }

// TestAHiderIsToldOfHidingAndOthersKeepRunning: plugin.hide and plugin.show reach a Handler that is
// a Hider, in order; one that is not ignores them and runs on.
func TestAHiderIsToldOfHidingAndOthersKeepRunning(t *testing.T) {
	h := &hider{}
	hs := start(t, h)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: Protocol, Width: 10, Height: 5}))
	hs.next(t)
	hs.send(t, MethodHide, EmptyParams())
	hs.send(t, MethodShow, EmptyParams())
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	if got := h.all(); !reflect.DeepEqual(got, []string{"open 10x5  ", "hide", "show", "close"}) {
		t.Fatalf("a Hider's calls = %q", got)
	}
	r := &recorder{}
	hs = start(t, r)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: Protocol, Width: 10, Height: 5}))
	hs.next(t)
	hs.send(t, MethodHide, EmptyParams())
	hs.send(t, MethodKey, KeyParams(Key{Key: "a"}))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	if got := r.all(); !reflect.DeepEqual(got, []string{"open 10x5  ", `key a "" ctrl=false`, "close"}) {
		t.Fatalf("a Handler that is not a Hider: %q", got)
	}
}

var dark = Theme{Name: "dark", Colors: map[string]string{"app.window": "#1c1c1c"}}

// TestServeAnswersTheHandshakeAndCallsTheHandlerInOrder: plugin.open is answered with host.ready and
// the plugin's protocol, then every notification reaches the Handler in the order sent, and
// plugin.close ends Serve cleanly.
func TestServeAnswersTheHandshakeAndCallsTheHandlerInOrder(t *testing.T) {
	r := &recorder{}
	hs := start(t, r)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: Protocol, Width: 40, Height: 20, Theme: dark}))
	ready := hs.next(t)
	if p, err := ReadReady(ready.params); ready.method != MethodReady || err != nil || p != Protocol {
		t.Fatalf("the answer = %s %v (%v), want %s %d", ready.method, p, err, MethodReady, Protocol)
	}
	hs.send(t, MethodKey, KeyParams(Key{Key: "a", Text: "a"}))
	hs.send(t, MethodKey, KeyParams(Key{Key: "Up", Ctrl: true}))
	hs.send(t, MethodResize, ResizeParams(50, 22))
	hs.send(t, MethodTheme, ThemeParams(Theme{Name: "retro"}))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatalf("ServeConn = %v, want nil after plugin.close", err)
	}
	want := []string{"open 40x20 dark #1c1c1c", `key a "a" ctrl=false`, `key Up "" ctrl=true`,
		"resize 50x22", "theme retro", "close"}
	if got := r.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the Handler's calls:\n got %q\nwant %q", got, want)
	}
}

// TestServeOpensNothingForAnotherProtocol: a host of another protocol is told this one, and nothing
// opens, so nothing closes either.
func TestServeOpensNothingForAnotherProtocol(t *testing.T) {
	r := &recorder{}
	hs := start(t, r)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: Protocol + 1, Width: 40, Height: 20}))
	if p, _ := ReadReady(hs.next(t).params); p != Protocol {
		t.Fatalf("host.ready said %d, want %d", p, Protocol)
	}
	hs.send(t, MethodKey, KeyParams(Key{Key: "a"}))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	if got := r.all(); len(got) != 0 {
		t.Fatalf("a refused protocol reached the Handler: %q", got)
	}
}

// TestAHandlerSlowerThanTheKeysDoesNotOverflowTheLink: more keys than the link's queue holds (1024),
// sent faster than the Handler takes them, all arrive, in order — Serve's own queue takes them, where
// the link's would overflow and end the link. On one CPU, as a busy machine is: there the link's
// reader outruns its dispatcher on any burst larger than its queue, and only the link's
// backpressure keeps it alive (golib #163); with more, the race is the scheduler's, and the cell
// would catch a regression only sometimes.
func TestAHandlerSlowerThanTheKeysDoesNotOverflowTheLink(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const n = 3000
	r := &recorder{keyLag: 200 * time.Microsecond}
	hs := start(t, r)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: Protocol, Width: 10, Height: 5}))
	hs.next(t)
	for i := range n {
		hs.send(t, MethodKey, KeyParams(Key{Key: fmt.Sprint(i)}))
	}
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatalf("ServeConn = %v", err)
	}
	got := r.all()
	if len(got) != n+2 {
		t.Fatalf("%d calls, want %d (open, %d keys, close)", len(got), n+2, n)
	}
	for i := range n {
		if want := fmt.Sprintf(`key %d "" ctrl=false`, i); got[i+1] != want {
			t.Fatalf("call %d = %q, want %q", i+1, got[i+1], want)
		}
	}
}

// TestTheHostGoneClosesTheHandler: the host's end closing (the TUI gone) is Handler.Close, and Serve
// returns without an error: a host that goes is not the plugin's fault.
func TestTheHostGoneClosesTheHandler(t *testing.T) {
	r := &recorder{}
	hs := start(t, r)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: Protocol, Width: 10, Height: 5}))
	hs.next(t)
	hs.link.Close()
	if err := hs.served(t); err != nil {
		t.Fatalf("ServeConn = %v, want nil when the host goes", err)
	}
	if got := r.all(); !reflect.DeepEqual(got, []string{"open 10x5  ", "close"}) {
		t.Fatalf("the Handler's calls = %q", got)
	}
}

// TestThePeerSendsFramesTitlesAndClose: what a plugin sends through its Peer reaches the host as the
// protocol's notifications.
func TestThePeerSendsFramesTitlesAndClose(t *testing.T) {
	r := &recorder{onOpen: func(p *Peer) {
		f := NewFrame(6, 2)
		f.Text(0, 0, "hi", Style{FG: "brightwhite", Bold: true})
		f.Set(5, 1, '█', Style{BG: "#ff0000"})
		_ = p.Title("Game over")
		_ = p.Frame(f)
		_ = p.Close()
	}}
	hs := start(t, r)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: Protocol, Width: 6, Height: 2}))
	hs.next(t) // host.ready
	title := hs.next(t)
	if s, _ := ReadTitle(title.params); title.method != MethodTitle || s != "Game over" {
		t.Fatalf("got %s %q, want the title", title.method, s)
	}
	frame := hs.next(t)
	rows, dropped, err := ReadFrame(frame.params)
	if frame.method != MethodFrame || err != nil || dropped != 0 {
		t.Fatalf("got %s (%v, %d dropped)", frame.method, err, dropped)
	}
	want := []Row{
		{{Text: "hi", Style: Style{FG: "brightwhite", Bold: true}}, {Text: "    "}},
		{{Text: "     "}, {Text: "█", Style: Style{BG: "#ff0000"}}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("the frame:\n got %#v\nwant %#v", rows, want)
	}
	if closing := hs.next(t); closing.method != MethodHostClose {
		t.Fatalf("got %s, want %s", closing.method, MethodHostClose)
	}
}

// TestServeAnswersEachProtocolInItsRange: a host speaking 1 (a 0209-era manifest) or 2 is answered
// with that same protocol, and the Handler opens; one below or above the range is told the newest,
// and nothing opens.
func TestServeAnswersEachProtocolInItsRange(t *testing.T) {
	for _, c := range []struct {
		protocol, answer int
		opens            bool
	}{{1, 1, true}, {2, 2, true}, {0, Protocol, false}, {3, Protocol, false}} {
		r := &recorder{}
		hs := start(t, r)
		hs.send(t, MethodOpen, OpenParams(Open{Protocol: c.protocol, Width: 40, Height: 20}))
		if p, err := ReadReady(hs.next(t).params); err != nil || p != c.answer {
			t.Errorf("protocol %d: host.ready said %d (%v), want %d", c.protocol, p, err, c.answer)
		}
		hs.send(t, MethodClose, EmptyParams())
		if err := hs.served(t); err != nil {
			t.Fatal(err)
		}
		if opened := len(r.all()) > 0; opened != c.opens {
			t.Errorf("protocol %d: opened %v, want %v (%q)", c.protocol, opened, c.opens, r.all())
		}
	}
}

// service is a protocol-2 plugin with no dialog, as one is written: Base, plus every optional
// interface. It writes down what it is sent.
type service struct {
	Base
	rec recorder
}

func (s *service) Open(p *Peer, o Open) { s.rec.Open(p, o) }
func (s *service) Close()               { s.rec.Close() }
func (s *service) Command(id string)    { s.rec.add("command " + id) }
func (s *service) Focus(focused bool)   { s.rec.add(fmt.Sprintf("focus %v", focused)) }
func (s *service) Document(d Document) {
	s.rec.add(fmt.Sprintf("document %s v%d %d bytes", d.Path, d.Version, len(d.Text)))
}

// TestTheOptionalInterfacesTakeProtocolTwo: a service opens with no size, and receives commands,
// focus and documents through its optional interfaces; a Handler with none of them is sent the same
// notifications and drops them, running on.
func TestTheOptionalInterfacesTakeProtocolTwo(t *testing.T) {
	s := &service{}
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 2}))
	hs.next(t)
	hs.send(t, MethodCommand, CommandParams("open"))
	hs.send(t, MethodFocus, FocusParams(true))
	hs.send(t, MethodFocus, FocusParams(false))
	hs.send(t, MethodDocument, DocumentParams(Document{Path: "a.md", Text: "hello", Version: 3}))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	want := []string{"open 0x0  ", "command open", "focus true", "focus false", "document a.md v3 5 bytes", "close"}
	if got := s.rec.all(); !reflect.DeepEqual(got, want) {
		t.Errorf("the service's calls:\n got %q\nwant %q", got, want)
	}

	r := &recorder{}
	hs = start(t, r)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 2, Width: 10, Height: 5}))
	hs.next(t)
	hs.send(t, MethodCommand, CommandParams("open"))
	hs.send(t, MethodDocument, DocumentParams(Document{Path: "a.md", Version: 1}))
	hs.send(t, MethodKey, KeyParams(Key{Key: "a", Text: "a"}))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	if got := r.all(); !reflect.DeepEqual(got, []string{"open 10x5  ", `key a "a" ctrl=false`, "close"}) {
		t.Errorf("a plain Handler's calls: %q", got)
	}
}

// slowReader takes each document slowly, and writes down its version.
type slowReader struct {
	recorder
	lag time.Duration
}

func (s *slowReader) Document(d Document) {
	time.Sleep(s.lag)
	s.add(fmt.Sprintf("document v%d", d.Version))
}

// TestASlowReaderGetsOnlyTheLatestDocument: documents sent much faster than the reader takes them
// are coalesced: the reader sees versions strictly rising, ends on the newest, and handles far fewer
// than were sent; the keys among them all arrive, in order.
func TestASlowReaderGetsOnlyTheLatestDocument(t *testing.T) {
	const n = 50
	s := &slowReader{lag: 20 * time.Millisecond}
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 2, Width: 10, Height: 5}))
	hs.next(t)
	for i := 1; i <= n; i++ {
		hs.send(t, MethodDocument, DocumentParams(Document{Path: "a.md", Text: strings.Repeat("x", 64<<10), Version: i}))
		hs.send(t, MethodKey, KeyParams(Key{Key: fmt.Sprint(i)}))
	}
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	var versions []int
	keys := 0
	for _, c := range s.all() {
		var v int
		if _, err := fmt.Sscanf(c, "document v%d", &v); err == nil {
			versions = append(versions, v)
		} else if strings.HasPrefix(c, "key ") {
			keys++
			if want := fmt.Sprintf(`key %d "" ctrl=false`, keys); c != want {
				t.Fatalf("key %d is %q, want %q", keys, c, want)
			}
		}
	}
	if keys != n {
		t.Errorf("%d keys arrived, want %d", keys, n)
	}
	if len(versions) == 0 || versions[len(versions)-1] != n {
		t.Fatalf("documents %v: want the last to be v%d", versions, n)
	}
	for i := 1; i < len(versions); i++ {
		if versions[i] <= versions[i-1] {
			t.Fatalf("documents %v: versions not strictly rising", versions)
		}
	}
	if len(versions) > n/5 {
		t.Errorf("%d of %d documents handled: the waiting ones were not replaced", len(versions), n)
	}
}

// TestADocumentIsBoundedByItsEncoding (ADR 1791268009 §2.3): the bound is the whole notification
// as the link writes it, not the text, and it is the link's own limit. A document that encodes to
// exactly MaxMessageBytes is sent whole and arrives; one byte more, which the link would refuse to
// send, is too large, keeping its path, workspace and version; neither ends the link.
func TestADocumentIsBoundedByItsEncoding(t *testing.T) {
	sized := func(target int) Document {
		d := Document{Path: "notes/a.md", Workspace: "kb", Cursor: Position{1, 1},
			Selection: []Range{{Position{1, 1}, Position{1, 2}}}, Version: 4}
		d.Text = strings.Repeat("x", target)
		n, err := EncodedSize(MethodDocument, DocumentParams(d))
		if err != nil {
			t.Fatal(err)
		}
		d.Text = strings.Repeat("x", target+(target-n)) // a long string's header is fixed: one byte a character
		if n, _ := EncodedSize(MethodDocument, DocumentParams(d)); n != target {
			t.Fatalf("sized to %d, encodes to %d", target, n)
		}
		return d
	}
	at := FitDocument(sized(MaxMessageBytes))
	over := FitDocument(sized(MaxMessageBytes + 1))
	if at.TooLarge || len(at.Text) == 0 {
		t.Fatalf("a document at the bound is too large")
	}
	if want := (Document{Path: "notes/a.md", Workspace: "kb", Version: 4, TooLarge: true}); !reflect.DeepEqual(over, want) {
		t.Fatalf("one byte over: %+v, want %+v", over, want)
	}

	s := &service{}
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 2}))
	hs.next(t)
	hs.send(t, MethodDocument, DocumentParams(at))
	hs.send(t, MethodCommand, CommandParams("after-the-whole-one"))
	// handled before the next is sent: a document still waiting would be replaced by it
	for deadline := time.Now().Add(5 * time.Second); len(s.rec.all()) < 3; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the whole document never arrived: %q", s.rec.all())
		}
	}
	hs.send(t, MethodDocument, DocumentParams(over))
	hs.send(t, MethodCommand, CommandParams("after-the-large-one"))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatalf("ServeConn = %v", err)
	}
	got := s.rec.all()
	want := []string{"open 0x0  ", fmt.Sprintf("document notes/a.md v4 %d bytes", len(at.Text)), "command after-the-whole-one",
		"document notes/a.md v4 0 bytes", "command after-the-large-one", "close"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the service's calls:\n got %q\nwant %q", got, want)
	}
}
