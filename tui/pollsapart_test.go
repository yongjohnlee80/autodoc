package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// heldVerbs is a module whose calls wait until released, each telling the cell it holds it: the
// window's long-polls (inbox.watch, mail.fallbacks) and an ordinary call (park.wait). A call the
// cell made says so in its tag ("cell…": the watch's page, the fallbacks' scope, park.wait's tag);
// the window's own inbox.watch waits as the daemon's does, its timeout or its connection's end.
type heldVerbs struct {
	held    chan heldCall
	release chan struct{} // closed: every call of the cell's answers
	once    sync.Once
}

// heldCall is one call the daemon holds for the cell: its verb, and the connection and ctx it
// came on (module.ConnFrom: the same value for every call of one connection).
type heldCall struct {
	verb string
	conn any
	ctx  context.Context
}

func newHeldVerbs() *heldVerbs {
	return &heldVerbs{held: make(chan heldCall, 64), release: make(chan struct{})}
}

func (p *heldVerbs) free() { p.once.Do(func() { close(p.release) }) }

func (*heldVerbs) Name() string { return "held" }

func (*heldVerbs) Errors() []module.PublicError { return nil }

// hold holds a call of the cell's until it is released or its connection ends, and any other
// call (the window's own) for its timeout.
func (p *heldVerbs) hold(ctx context.Context, verb, tag string, timeout int64) {
	if !strings.HasPrefix(tag, "cell") {
		select {
		case <-time.After(time.Duration(timeout) * time.Millisecond):
		case <-ctx.Done():
		}
		return
	}
	p.held <- heldCall{verb, module.ConnFrom(ctx), ctx}
	select {
	case <-p.release:
	case <-ctx.Done():
	}
}

func (p *heldVerbs) Verbs() []module.Verb {
	type waitParams struct {
		Tag string `param:"tag,optional" doc:"which call"`
	}
	type watchParams struct {
		Cursor  int64  `param:"cursor" doc:"the cursor"`
		Timeout int64  `param:"timeout,optional" doc:"ms"`
		Page    string `param:"page,optional" doc:"the snapshot's page"`
	}
	type fallbacksParams struct {
		Scope   string `param:"scope" doc:"the scope"`
		Run     string `param:"run" doc:"the run"`
		Epoch   int64  `param:"epoch" doc:"the epoch"`
		Timeout int64  `param:"timeout,optional" doc:"ms"`
	}
	c := func(verb string, effect module.Effect) module.Contract {
		return module.Contract{Verb: verb, Since: 1, Scope: module.ScopeLocalPeer, Audience: module.AudienceUser, Effect: effect,
			Result: "{}", Doc: "a cell's " + verb + ", held until released"}
	}
	return []module.Verb{
		module.Handle(c("park.wait", module.EffectRead), func(ctx context.Context, w waitParams) (struct{}, error) {
			p.hold(ctx, "park.wait", w.Tag, 0)
			return struct{}{}, nil
		}),
		module.Handle(c("inbox.watch", module.EffectRead), func(ctx context.Context, w watchParams) (map[string]any, error) {
			p.hold(ctx, "inbox.watch", w.Page, w.Timeout)
			return map[string]any{"cursor": w.Cursor}, nil
		}),
		module.Handle(c("mail.fallbacks", module.EffectWrite), func(ctx context.Context, w fallbacksParams) (map[string]any, error) {
			p.hold(ctx, "mail.fallbacks", w.Scope, w.Timeout)
			return map[string]any{"jobs": []any{}}, nil
		}),
	}
}

// heldTUI is a window on a daemon serving the held verbs, connected, its notes listed; seams, when
// set, sets the session's test seams before it connects. The held calls are released before the
// window and the daemon stop.
func heldTUI(t *testing.T, seams func(*Session)) (*running, *heldVerbs) {
	t.Helper()
	p := newHeldVerbs()
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "the note's body\n"}}, daemonOpts{modules: []module.Module{p}})
	sess := NewSession(d.sock, nil)
	if seams != nil {
		seams(sess)
	}
	r := runTUI(t, sess, Options{})
	t.Cleanup(p.free) // registered last, so it runs first: before the window and the daemon stop
	r.ready(t)
	return r, p
}

// callFromWorker makes verb's call from a worker, as the window's own background work does.
func (r *running) callFromWorker(verb string, params ...any) {
	go func() { _, _ = r.h.call(context.Background(), verb, params...) }()
}

// heldCalls are the next n calls the daemon holds for the cell, within daemonWait.
func heldCalls(t *testing.T, p *heldVerbs, n int) []heldCall {
	t.Helper()
	var out []heldCall
	for len(out) < n {
		select {
		case c := <-p.held:
			out = append(out, c)
		case <-time.After(daemonWait):
			t.Fatalf("only %d of the %d calls reached the daemon's handlers within %s", len(out), n, daemonWait)
		}
	}
	return out
}

// openedA says whether a.md is open in the editor, as the daemon's read answered it.
func (r *running) openedA() bool { f := r.file(); return f.open && f.path == "a.md" && !f.dirty }

// TestAnOpenIsAnsweredWhileTheWindowsLongPollsWait: the window's two long-polls (the inbox's watch
// and the crew host's fallback jobs) wait on a connection of their own, not on the one its other
// calls take: with both held and six ordinary calls held too, a note still opens. On one
// connection those are the eight requests the daemon reads of it at once, and the open would wait
// for one of them to end. The window's client token and generation are the primary connection's
// throughout.
func TestAnOpenIsAnsweredWhileTheWindowsLongPollsWait(t *testing.T) {
	t.Parallel()
	r, p := heldTUI(t, nil)
	token, gen := r.h.session.Client(), r.h.session.Gen()
	r.callFromWorker("inbox.watch", int64(0), int64(25000), "cell-watch")
	r.callFromWorker("mail.fallbacks", "cell-scope", "run", int64(1), int64(25000))
	for i := range 6 {
		r.callFromWorker("park.wait", fmt.Sprintf("cell-%d", i))
	}
	conns := map[string]map[any]bool{}
	for _, c := range heldCalls(t, p, 8) {
		if conns[c.verb] == nil {
			conns[c.verb] = map[any]bool{}
		}
		conns[c.verb][c.conn] = true
	}
	if len(conns["inbox.watch"]) != 1 || len(conns["mail.fallbacks"]) != 1 || len(conns["park.wait"]) != 1 {
		t.Fatalf("connections per verb: %d inbox.watch, %d mail.fallbacks, %d park.wait; want one each",
			len(conns["inbox.watch"]), len(conns["mail.fallbacks"]), len(conns["park.wait"]))
	}
	var watch, fallbacks, ordinary any
	for c := range conns["inbox.watch"] {
		watch = c
	}
	for c := range conns["mail.fallbacks"] {
		fallbacks = c
	}
	for c := range conns["park.wait"] {
		ordinary = c
	}
	if watch != fallbacks || watch == ordinary {
		t.Fatalf("the long-polls on connections %v and %v, the ordinary calls on %v: want the long-polls on one connection, not the ordinary calls'",
			watch, fallbacks, ordinary)
	}
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitWithin(t, "a.md opened with the long-polls and six calls held", daemonWait, r.openedA)
	if got := r.editorText(); got != "the note's body\n" {
		t.Errorf("a.md opened holding %q", got)
	}
	if r.h.session.Client() != token || r.h.session.Gen() != gen {
		t.Errorf("after the long-polls' connection: token %q gen %d; want the primary's %q %d", r.h.session.Client(), r.h.session.Gen(), token, gen)
	}
}

// TestTheConnectionsBoundStillHoldsOrdinaryCalls: ordinary calls share the primary connection, and
// its bound is the daemon's: with seven held an open is answered; with eight, the daemon reads no
// further request of that connection, so the open waits until one of them ends, and lands then.
// The bound is kept on purpose (the daemon's retention of decoded requests is bounded by it); only
// the long-polls wait apart.
func TestTheConnectionsBoundStillHoldsOrdinaryCalls(t *testing.T) {
	for _, n := range []int{7, 8} {
		t.Run(fmt.Sprintf("%d held", n), func(t *testing.T) {
			t.Parallel()
			sent := make(chan struct{}, 4)
			r, p := heldTUI(t, func(sess *Session) {
				sess.SetBeforeCall(func(method string, params []any) {
					if method == "doc.read" && len(params) > 1 && params[1] == "a.md" {
						select {
						case sent <- struct{}{}:
						default:
						}
					}
				})
			})
			for i := range n {
				r.callFromWorker("park.wait", fmt.Sprintf("cell-%d", i))
			}
			heldCalls(t, p, n)
			r.h.p.Post(func() { r.h.openPath("a.md") })
			if n < 8 {
				r.waitWithin(t, fmt.Sprintf("a.md opened with %d calls held", n), daemonWait, r.openedA)
				return
			}
			select {
			case <-sent:
			case <-time.After(daemonWait):
				t.Fatalf("the open's doc.read was never sent")
			}
			time.Sleep(300 * time.Millisecond * raceFactor) // the read is sent: what the daemon does with it
			if r.openedA() {
				t.Fatal("with 8 calls held on the connection, a.md opened: the bound the daemon reads a connection's requests in is gone")
			}
			p.free()
			r.waitWithin(t, "a.md opened once the held calls ended", daemonWait, r.openedA)
		})
	}
}

// TestTheWindowsLongPollsEndWithItsConnection: a reconnect ends the long-polls held on the old
// connection's polling connection (the daemon ends their calls), and the next one waits on a new
// one; closing the session ends them too.
func TestTheWindowsLongPollsEndWithItsConnection(t *testing.T) {
	t.Parallel()
	r, p := heldTUI(t, nil)
	r.callFromWorker("inbox.watch", int64(0), int64(25000), "cell-1")
	first := heldCalls(t, p, 1)[0]
	ctx, cancel := context.WithTimeout(context.Background(), daemonWait)
	defer cancel()
	if err := r.h.session.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.ctx.Done():
	case <-time.After(daemonWait):
		t.Fatal("after a reconnect, the daemon still holds the long-poll of the connection before")
	}
	r.callFromWorker("inbox.watch", int64(0), int64(25000), "cell-2")
	second := heldCalls(t, p, 1)[0]
	if second.conn == first.conn {
		t.Fatal("after a reconnect, a long-poll went on the connection before's polling connection")
	}
	r.h.session.Close()
	select {
	case <-second.ctx.Done():
	case <-time.After(daemonWait):
		t.Fatal("after the session closed, the daemon still holds its long-poll")
	}
}
