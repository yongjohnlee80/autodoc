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
// esc = "hide"; a card's panel closed) and shown again: a game pauses. A Handler that is not one
// keeps running, unseen.
type Hider interface {
	Hide()
	Show()
}

// The optional interfaces of protocol 2 (ADR 1791268009) and 4. Serve probes the Handler for each;
// a notification no interface takes is dropped. A plugin implements the ones its manifest declares.
type (
	// DocumentReader takes the feed ([feed] document = true): the open note, after each pause in
	// editing. Only the latest waits for it: a newer document replaces one not yet handled. From
	// protocol 4 a command carries the note as it stood when it was chosen, and the reader is
	// handed that one first, just before the command runs.
	DocumentReader interface{ Document(d Document) }
	// Commander takes the manifest's [[commands]], by id.
	Commander interface{ Command(id string) }
	// CommandArgser takes a [[commands]] id with its arguments (protocol 7): `:autodb query …` from
	// the plugin's Lua package. A Handler that is not one is handed Command(id), the arguments dropped.
	CommandArgser interface {
		CommandArgs(id string, args []string)
	}
	// Focuser is a non-modal card told it took the keys (true) or gave them back (false).
	Focuser interface{ Focus(focused bool) }
	// PointerHandler takes view-local left presses and vertical wheel movement (protocol 6).
	PointerHandler interface{ Pointer(Pointer) }
	// TableFetcher serves the rows of the tables it sends (protocol 4, [table]): FetchRows is the
	// host asking for up to f.N more of table f.ID, answered with Peer.Rows (or Peer.TableEnd);
	// the host asks again only after the answer. TableClosed is the table gone: its panel closed,
	// or a newer table replaced it.
	TableFetcher interface {
		FetchRows(f TableFetch)
		TableClosed(id string)
	}
	// SurfaceHandler draws a pixel surface (protocol 10, [surface]): Surface is each generation of
	// it (the first after Open; again at a resize, a scale or a caps change), and SurfacePointer the
	// pointer on it in dp. Consecutive moves reach it coalesced to the latest, and consecutive
	// scrolls summed. A host that draws no pixels never calls Surface: the plugin draws cells.
	SurfaceHandler interface {
		Surface(s *Surface, info SurfaceInfo)
		SurfacePointer(p SurfacePointer)
	}
	// Paster takes the user's paste chord on a focused surface (protocol 10).
	Paster interface{ Paste(text string) }
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
	// closeReason is plugin.close's reason (protocol 7), set before Close is called
	closeReason atomic.Value
	// surface is the side channel's, when the host gave one (protocol 10); nil otherwise
	surface *Surface
}

// ErrNoSurface is a surface call on a host that gave no pixel surface (protocol 10, [surface]).
var ErrNoSurface = errors.New("plugin: the host gave no pixel surface")

// SetCaret tells the host where the surface's text caret is, in frame-local dp, for its IME: it
// draws the preedit there and places the candidate window by it. The zero Caret is none.
func (p *Peer) SetCaret(c Caret) error {
	if p.surface == nil {
		return ErrNoSurface
	}
	return p.link.Notify(p.ctx, MethodCaret, CaretParams(c))
}

// SetPointerShape asks for the pointer's shape over the surface: "default", "text", "hand",
// "crosshair", "move", "resize-ew", "resize-ns" or "not-allowed".
func (p *Peer) SetPointerShape(shape string) error {
	if p.surface == nil {
		return ErrNoSurface
	}
	return p.link.Notify(p.ctx, MethodPointerShape, PointerShapeParams(shape))
}

// WriteClipboard writes text to the clipboard. A plugin reads it only by the user's paste (Paster).
func (p *Peer) WriteClipboard(text string) error {
	if p.surface == nil {
		return ErrNoSurface
	}
	return p.link.Notify(p.ctx, MethodClipboardWrite, ClipboardWriteParams(text))
}

// ErrNoSession is a slot set on a host that opened the plugin at a protocol before 7.
var ErrNoSession = errors.New("plugin: the host speaks a protocol before 7, without a session slot")

// SetSession replaces this plugin's session slot (protocol 7). The host keeps it in memory, never on
// disk, until AutoDoc quits; Open.Session is it at the plugin's next start in this run of AutoDoc. A
// nil error means it was sent, not that the host kept it: the host drops a slot over the bounds, and
// one sent after the plugin began to stop, so a plugin sets its slot when it signs in, never in
// Close.
func (p *Peer) SetSession(values map[string]string) error {
	if p.protocol.Load() < 7 {
		return ErrNoSession
	}
	if err := CheckSession(values); err != nil {
		return err
	}
	return p.link.Notify(p.ctx, MethodSession, SessionParams(values))
}

// Notify posts n (protocol 8): a toast kept in the history, or with an id one updated in place
// while Ongoing and ended by a post without it (notify.go). As SetSession, the negotiated protocol
// is checked first, then n; a nil error means it was sent: the host drops a notice from a run that
// is stopping, and an ongoing one past MaxActiveNotices.
func (p *Peer) Notify(n Notice) error {
	if p.protocol.Load() < 8 {
		return ErrNoNotify
	}
	if err := CheckNotice(n); err != nil {
		return err
	}
	return p.link.Notify(p.ctx, MethodNotify, NotifyParams(n))
}

// CloseReason is why the host is closing the plugin (protocol 7: CloseQuit, CloseOff, …), for Close
// to read; "" before protocol 7, or when the host closed it without a plugin.close.
func (p *Peer) CloseReason() string {
	s, _ := p.closeReason.Load().(string)
	return s
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
	// ErrNoTables is a table sent to a host that opened the plugin at a protocol before 4, which has
	// no host.table.
	ErrNoTables = errors.New("plugin: the host speaks a protocol before 4, without tables")
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
// A card's panel closes instead, and the plugin runs on: the host answers with plugin.hide.
func (p *Peer) Close() error { return p.link.Notify(p.ctx, MethodHostClose, EmptyParams()) }

// Table shows t in AutoDoc's results panel (protocol 4; the manifest declares [table]), replacing
// the table shown. Give each table a new ID (a query run again is a new table): a fetch for the
// table shown may still be on its way, so the host refuses a table that reuses the shown table's
// ID, keeping the one shown. Rows are taken only as the answer to a FetchRows. Keep each message
// under the link's limit, MaxMessageBytes as EncodedSize measures it: size the rows by their
// encoded bytes, not their count alone.
//
// t.Kinds go only to a host that opened the plugin at protocol 5 or later; below it the table is
// sent without them, every cell text, as that host would read it.
func (p *Peer) Table(t TableOpen) error {
	if p.protocol.Load() < 4 {
		return ErrNoTables
	}
	if p.protocol.Load() < 5 {
		t.Kinds = nil
	}
	return p.link.Notify(p.ctx, MethodTable, TableParams(t))
}

// Rows sends the next rows of table id, answering a FetchRows; more says rows remain.
func (p *Peer) Rows(id string, rows [][]string, more bool) error {
	return p.RowsKinds(id, rows, nil, more)
}

// RowsKinds is Rows with each row's kinds (protocol 5; see TableOpen.Kinds): kinds[i] is rows[i]'s,
// one byte per field. Below protocol 5 the rows go without them.
func (p *Peer) RowsKinds(id string, rows [][]string, kinds []string, more bool) error {
	if p.protocol.Load() < 4 {
		return ErrNoTables
	}
	if p.protocol.Load() < 5 {
		kinds = nil
	}
	return p.link.Notify(p.ctx, MethodRows, RowsKindsParams(id, rows, kinds, more))
}

// TableEnd ends table id's rows early: no more will come, and err, when not nil, says why (a query
// that failed on its third page, say). It answers a FetchRows as Rows does.
func (p *Peer) TableEnd(id string, err error) error {
	if p.protocol.Load() < 4 {
		return ErrNoTables
	}
	why := ""
	if err != nil {
		why = err.Error()
	}
	return p.link.Notify(p.ctx, MethodTableEnd, TableEndParams(id, why))
}

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
	// the side channel first, before the handler can start a process: close-on-exec at once
	peer := &Peer{ctx: ctx, link: link, calls: calls, surface: openSurface()}
	if peer.surface != nil {
		defer peer.surface.Close() // after the handler's Close: its own goroutines see the surface end
	}
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
		case MethodPointer:
			if ph, ok := h.(PointerHandler); ok && opened && peer.protocol.Load() >= 6 {
				if p, err := ReadPointer(n.params); err == nil {
					ph.Pointer(p)
				}
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
			id, err := ReadCommand(n.params)
			if err != nil || !opened {
				break
			}
			// the note the command was chosen on, handed over in this same step: nothing the host
			// sent later (a newer feed document) runs between the two
			if r, ok := h.(DocumentReader); ok {
				if d, ok := ReadCommandDocument(n.params); ok {
					r.Document(d)
				}
			}
			if c, ok := h.(CommandArgser); ok {
				c.CommandArgs(id, ReadCommandArgs(n.params))
			} else if c, ok := h.(Commander); ok {
				c.Command(id)
			}
		case MethodFetch:
			if tf, ok := h.(TableFetcher); ok && opened {
				if f, err := ReadFetch(n.params); err == nil {
					tf.FetchRows(f)
				}
			}
		case MethodTableGone:
			if tf, ok := h.(TableFetcher); ok && opened {
				if id, err := ReadTableGone(n.params); err == nil {
					tf.TableClosed(id)
				}
			}
		case MethodSurface:
			if info, err := ReadSurface(n.params); err == nil && opened && peer.surface != nil {
				peer.surface.setInfo(info)
				if sh, ok := h.(SurfaceHandler); ok {
					sh.Surface(peer.surface, info)
				}
			}
		case MethodSurfacePointer:
			if sh, ok := h.(SurfaceHandler); ok && opened {
				if p, err := ReadSurfacePointer(n.params); err == nil {
					sh.SurfacePointer(p)
				}
			}
		case MethodPaste:
			if ps, ok := h.(Paster); ok && opened {
				if text, err := ReadPaste(n.params); err == nil {
					ps.Paste(text)
				}
			}
		case MethodClose:
			peer.closeReason.Store(ReadClose(n.params))
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
	if !q.coalesce(method, params) {
		q.items = append(q.items, note{method, params})
	}
	q.mu.Unlock()
	q.signal()
}

// coalesce folds a surface pointer event into the one waiting at the tail (under mu): a move
// replaces a move, and a scroll's deltas add to a scroll's, so a busy handler never reads a
// backlog of stale positions. A press, release or anything else ends the run.
func (q *queue) coalesce(method string, params []any) bool {
	if method != MethodSurfacePointer || len(q.items) == 0 {
		return false
	}
	last := &q.items[len(q.items)-1]
	if last.method != MethodSurfacePointer {
		return false
	}
	a, errA := arg(last.params)
	b, errB := arg(params)
	if errA != nil || errB != nil || str(a, "kind") != str(b, "kind") {
		return false
	}
	switch str(b, "kind") {
	case "move":
		last.params = params
		return true
	case "scroll":
		adx, _ := dpNum(a, "dx")
		ady, _ := dpNum(a, "dy")
		bdx, _ := dpNum(b, "dx")
		bdy, _ := dpNum(b, "dy")
		b["dx"], b["dy"] = adx+bdx, ady+bdy
		last.params = params
		return true
	}
	return false
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
