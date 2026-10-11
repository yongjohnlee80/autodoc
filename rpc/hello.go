package rpc

import (
	"context"
	"sync"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// A session's first request is serialized behind its sys.hello. golib's gate runs on the
// connection's read loop, in order, but each request is served on its own goroutine: a verb a
// client pipelines behind its hello, without waiting for the answer, would reach the gate before the
// hello admitted the session and be refused as HandshakeRequired. The gate counts the hellos it lets
// through, and a request on a session not yet admitted waits for them to answer, helloWait at most:
// only that connection's read loop waits. Past the bound it is refused as before.

// sessHelloPending is the session's hellos the gate let through and their handlers have not answered.
const sessHelloPending = "hello-pending"

// helloWait bounds how long a request waits on its session's hellos.
var helloWait = 3 * time.Second

// hellos counts a session's pending hellos; zero closes when the count returns to none.
type hellos struct {
	mu   sync.Mutex
	n    int
	zero chan struct{}
}

func (h *hellos) add() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n++; h.n == 1 {
		h.zero = make(chan struct{})
	}
}

// done marks one answered; a hello the gate never counted (a handler called directly) is none.
func (h *hellos) done() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n == 0 {
		return
	}
	if h.n--; h.n == 0 {
		close(h.zero)
	}
}

// wait returns when none is pending, or after d.
func (h *hellos) wait(d time.Duration) {
	h.mu.Lock()
	if h.n == 0 {
		h.mu.Unlock()
		return
	}
	zero := h.zero
	h.mu.Unlock()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-zero:
	case <-t.C:
	}
}

// pendingHellos is the session's count, made on first use: by the gate, on the read loop, before
// any handler of the session can ask for it.
func pendingHellos(sess *golibrpc.Session) *hellos {
	h, _ := sess.Value(sessHelloPending).(*hellos)
	if h == nil {
		h = &hellos{}
		sess.SetValue(sessHelloPending, h)
	}
	return h
}

// awaitHellos waits until the session's pending hellos have answered, or helloWait has passed.
func awaitHellos(sess *golibrpc.Session) {
	if h, _ := sess.Value(sessHelloPending).(*hellos); h != nil {
		h.wait(helloWait)
	}
}

// beforeHello, when set (tests only), runs before each hello's handler: the seam that holds a hello
// back while the request behind it reaches the gate. It is package-global: a test that sets it must
// not run in parallel with any other test that starts a server.
var beforeHello func()

// helloAnswered marks a hello the gate let through as answered, however its handler ends.
func helloAnswered(h golibrpc.Handler) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		if req.Session != nil {
			if p, _ := req.Session.Value(sessHelloPending).(*hellos); p != nil {
				defer p.done()
			}
		}
		if beforeHello != nil {
			beforeHello()
		}
		return h(ctx, req)
	}
}
