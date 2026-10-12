package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Handler is a dialog plugin. Serve calls its methods one at a time, in the order the host sent
// them, on one goroutine of its own. A plugin with a clock of its own (a game's gravity) runs it on
// another goroutine and sends through the Peer, which is safe for concurrent use. A method may
// Peer.Call: the host's answer never waits behind it.
type Handler interface {
	// Open is the dialog opened: its size inside the border, and the theme. p sends frames.
	Open(p *Peer, o Open)
	// Key is a key typed into the dialog.
	Key(k Key)
	// Resize is the dialog's new size.
	Resize(w, h int)
	// Theme is the theme changed.
	Theme(t Theme)
	// Close is the dialog closing, or the host gone: save what is kept, stop, and return. The host
	// waits two seconds before it stops the process. It is called once, and only after Open.
	Close()
}

// Hider is a Handler that wants to know its dialog was hidden (Esc, when its manifest says
// esc = "hide") and shown again: a game pauses. A Handler that is not one keeps running, unseen.
type Hider interface {
	Hide()
	Show()
}

// The optional interfaces of protocol 2 (ADR 1791268009). Serve probes the Handler for each; a
// notification no interface takes is dropped. A plugin implements the ones its manifest declares.
type (
	// DocumentReader takes the feed ([feed] document = true): the open note, after each pause in
	// editing. Only the latest waits for it: a newer document replaces one not yet handled.
	DocumentReader interface{ Document(d Document) }
	// Commander takes the manifest's [[commands]], by id.
	Commander interface{ Command(id string) }
	// Focuser is a non-modal card told it took the keys (true) or gave them back (false).
	Focuser interface{ Focus(focused bool) }
)

// Base is a Handler that does nothing: a service plugin, which has no dialog, embeds it and adds
// the optional interfaces it needs.
type Base struct{}

func (Base) Open(*Peer, Open) {}
func (Base) Key(Key)          {}
func (Base) Resize(int, int)  {}
func (Base) Theme(Theme)      {}
func (Base) Close()           {}

// Peer is the host, as a plugin sends to it.
type Peer struct {
	ctx      context.Context
	link     *Link
	calls    *pendingCalls
	protocol atomic.Int64 // plugin.open's, once opened
}

// The errors of Peer.Call besides the host's own answer (a *CallError).
var (
	// ErrClosed is a call made, or waiting, when the link is gone.
	ErrClosed = errors.New("plugin: the link to the host is closed")
	// ErrCallTimeout is a call the host did not answer within CallTimeout. Its answer, if it comes,
	// is dropped.
	ErrCallTimeout = errors.New("plugin: the host did not answer the call in time")
	// ErrNoCalls is a call to a host that opened the plugin at a protocol before 3, which has no
	// host.call.
	ErrNoCalls = errors.New("plugin: the host speaks a protocol before 3, without host.call")
)

// callTimeout is CallTimeout, which a test shortens.
var callTimeout = CallTimeout

// Call asks the host to run a command of AutoDoc's catalog by its id, with args (protocol 3): one
// of the ids the manifest's calls = [...] declares. It waits for the host's answer, at most
// CallTimeout, and returns true, or false and why: a *CallError (refused, unknown, failed), or
// ErrCallTimeout, ErrClosed or ErrNoCalls. Each call has a number of its own, never one still
// waiting. It is safe from any goroutine, the Handler's own methods included: the answer is taken
// off the link as it arrives, never queued behind the method that waits for it.
func (p *Peer) Call(id string, args ...string) (bool, error) {
	if p.protocol.Load() < 3 {
		return false, ErrNoCalls
	}
	n, answer, err := p.calls.begin()
	if err != nil {
		return false, err
	}
	if err := p.link.Notify(p.ctx, MethodCall, CallParams(Call{Num: n, ID: id, Args: args})); err != nil {
		p.calls.cancel(n)
		return false, fmt.Errorf("%w: %w", ErrClosed, err)
	}
	// NOTE: a deadline the code can name: CallTimeout bounds the wait for an answer that may not
	// come (a host that dropped the call, or hangs), so a plugin never waits forever. The answer
	// itself is waited on, not polled; this only ends the wait when it does not arrive.
	t := time.NewTimer(callTimeout)
	defer t.Stop()
	select {
	case r := <-answer:
		if r.OK {
			return true, nil
		}
		return false, callError(r.Error)
	case <-t.C:
		p.calls.cancel(n)
		return false, ErrCallTimeout
	case <-p.calls.done:
		return false, ErrClosed
	}
}

// pendingCalls are a Peer's calls waiting for their answers, by number. The link's own goroutine
// resolves them, before the Handler's queue, so a Handler method waiting on one is never behind
// its answer.
type pendingCalls struct {
	mu      sync.Mutex
	next    int64
	waiting map[int64]chan Result
	closed  bool
	done    chan struct{} // closed with the link: every waiting call fails
}

func newPendingCalls() *pendingCalls {
	return &pendingCalls{waiting: map[int64]chan Result{}, done: make(chan struct{})}
}

// begin is a new call: its number, never one waiting, and where its answer comes.
func (c *pendingCalls) begin() (int64, chan Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, nil, ErrClosed
	}
	c.next++
	ch := make(chan Result, 1)
	c.waiting[c.next] = ch
	return c.next, ch, nil
}

// resolve hands a plugin.result to its call; one for no call waiting (late, or never asked) is
// dropped.
func (c *pendingCalls) resolve(params []any) {
	r, err := ReadResult(params)
	if err != nil {
		return
	}
	c.mu.Lock()
	ch := c.waiting[r.Num]
	delete(c.waiting, r.Num)
	c.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

// cancel forgets a call no longer waiting.
func (c *pendingCalls) cancel(n int64) {
	c.mu.Lock()
	delete(c.waiting, n)
	c.mu.Unlock()
}

// close fails every call waiting, and every call after.
func (c *pendingCalls) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
}

// Frame sends f, the whole dialog.
func (p *Peer) Frame(f *Frame) error { return p.link.Notify(p.ctx, MethodFrame, FrameParams(f.Rows())) }

// Title sets the dialog's title.
func (p *Peer) Title(title string) error {
	return p.link.Notify(p.ctx, MethodTitle, TitleParams(title))
}

// Close asks the host to close the dialog: it answers with plugin.close, and Handler.Close follows.
func (p *Peer) Close() error { return p.link.Notify(p.ctx, MethodHostClose, EmptyParams()) }

// Serve speaks the protocol on the plugin's stdin and stdout until the dialog closes, the host goes
// or ctx ends. It returns nil when the host closed the dialog. stdout is the protocol's: after Serve
// starts, os.Stdout is os.Stderr, which the host keeps in the plugin's log.
func Serve(ctx context.Context, h Handler) error {
	conn, err := stdio()
	if err != nil {
		return err
	}
	return ServeConn(ctx, conn, h)
}

// ServeConn is Serve over conn: a test's pipes, say.
func ServeConn(ctx context.Context, conn net.Conn, h Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	q, calls := newQueue(), newPendingCalls()
	// plugin.result goes straight to its call, every other notification to the Handler's queue
	link, err := NewLink(ctx, conn, func(method string, params []any) {
		if method == MethodResult {
			calls.resolve(params)
			return
		}
		q.push(method, params)
	})
	if err != nil {
		return err
	}
	defer link.Close()
	defer calls.close()
	peer := &Peer{ctx: ctx, link: link, calls: calls}
	go func() {
		select {
		case <-link.Done():
		case <-ctx.Done():
		}
		calls.close()
		q.end()
	}()

	opened := false
	defer func() {
		if opened {
			h.Close()
		}
	}()
	for {
		n, ok := q.pop()
		if !ok {
			break
		}
		switch n.method {
		case MethodOpen:
			o, err := ReadOpen(n.params)
			if err != nil {
				return err
			}
			// one in the range is answered with itself; another with the newest this SDK speaks,
			// which the host refuses, and closes: nothing is opened
			speaks := o.Protocol >= MinProtocol && o.Protocol <= Protocol
			answer := Protocol
			if speaks {
				answer = o.Protocol
			}
			if err := link.Notify(ctx, MethodReady, ReadyParams(answer)); err != nil {
				return err
			}
			if speaks && !opened {
				opened = true
				peer.protocol.Store(int64(o.Protocol))
				h.Open(peer, o)
			}
		case MethodKey:
			if k, err := ReadKey(n.params); err == nil && opened {
				h.Key(k)
			}
		case MethodResize:
			if w, hh, err := ReadResize(n.params); err == nil && opened {
				h.Resize(w, hh)
			}
		case MethodTheme:
			if t, err := ReadTheme(n.params); err == nil && opened {
				h.Theme(t)
			}
		case MethodHide, MethodShow:
			if hd, ok := h.(Hider); ok && opened {
				if n.method == MethodHide {
					hd.Hide()
				} else {
					hd.Show()
				}
			}
		case MethodFocus:
			if f, ok := h.(Focuser); ok && opened {
				if focused, err := ReadFocus(n.params); err == nil {
					f.Focus(focused)
				}
			}
		case MethodDocument:
			if r, ok := h.(DocumentReader); ok && opened {
				if d, err := ReadDocument(n.params); err == nil {
					r.Document(d)
				}
			}
		case MethodCommand:
			if c, ok := h.(Commander); ok && opened {
				if id, err := ReadCommand(n.params); err == nil {
					c.Command(id)
				}
			}
		case MethodClose:
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := link.Err(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("plugin: the host's link: %w", err)
	}
	return nil
}

type note struct {
	method string
	params []any
}

// queue is unbounded: the link's callback only appends, so a Handler slower than the keys it is sent
// never fills the link's own bounded queue, whose overflow would end the link. A document is state,
// not an event: one still waiting is dropped when a newer arrives, which takes the tail, so a slow
// DocumentReader holds at most the copy it is handling and one waiting. Nothing else is dropped or
// reordered.
type queue struct {
	mu    sync.Mutex
	items []note
	ended bool
	wake  chan struct{}
}

func newQueue() *queue { return &queue{wake: make(chan struct{}, 1)} }

func (q *queue) push(method string, params []any) {
	q.mu.Lock()
	if method == MethodDocument {
		q.items = slices.DeleteFunc(q.items, func(n note) bool { return n.method == MethodDocument })
	}
	q.items = append(q.items, note{method, params})
	q.mu.Unlock()
	q.signal()
}

func (q *queue) end() {
	q.mu.Lock()
	q.ended = true
	q.mu.Unlock()
	q.signal()
}

func (q *queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// pop is the next notification, in order; false once the queue has ended and is drained.
func (q *queue) pop() (note, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			n := q.items[0]
			q.items[0] = note{}
			q.items = q.items[1:]
			q.mu.Unlock()
			return n, true
		}
		ended := q.ended
		q.mu.Unlock()
		if ended {
			return note{}, false
		}
		<-q.wake
	}
}
