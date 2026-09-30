package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

// Handler is a dialog plugin. Serve calls its methods one at a time, in the order the host sent
// them, on one goroutine of its own. A plugin with a clock of its own (a game's gravity) runs it on
// another goroutine and sends through the Peer, which is safe for concurrent use.
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

// Peer is the host, as a plugin sends to it.
type Peer struct {
	ctx  context.Context
	link *Link
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
	q := newQueue()
	link, err := NewLink(ctx, conn, q.push)
	if err != nil {
		return err
	}
	defer link.Close()
	peer := &Peer{ctx: ctx, link: link}
	go func() {
		select {
		case <-link.Done():
		case <-ctx.Done():
		}
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
			if err := link.Notify(ctx, MethodReady, ReadyParams(Protocol)); err != nil {
				return err
			}
			// another protocol: the host refuses it, and closes; nothing is opened
			if o.Protocol == Protocol && !opened {
				opened = true
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
// never fills the link's own bounded queue, whose overflow would end the link.
type queue struct {
	mu    sync.Mutex
	items []note
	ended bool
	wake  chan struct{}
}

func newQueue() *queue { return &queue{wake: make(chan struct{}, 1)} }

func (q *queue) push(method string, params []any) {
	q.mu.Lock()
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
