package api

import (
	"container/list"
	"fmt"
	"maps"
	"math"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/enterprise/ops"
)

// THE REFUSALS' ACCOUNT (docs/decisions/server.md#a-limit-per-client). A request refused before it
// is read, for no valid token or for its client's limits, is counted, and said in the log once a
// minute as a summary, never one line each: a line a request is a line anyone can write, and a
// flood's lines bury every other.
//
//   - a client's refusals: one line a minute for the client, with how many of each kind;
//   - unauthorized requests: one line a minute, with how many and from how many addresses.
//
// The counts since the server started are the status's server.http.refused and the metrics'
// autodoc_http_refused_total, by reason and never by client.

// SummaryEvery is how often the refusals are said in the log, counted from the first refusal
// since the last summary.
const SummaryEvery = time.Minute

// summaryAddrs is how many addresses a summary tells apart: past it, it says "over" that many.
const summaryAddrs = 1000

// refusals counts the refused requests and says them in the log. It is safe for concurrent use.
type refusals struct {
	log   logger.Logger
	every time.Duration
	// after is time.AfterFunc, or a cell's: it runs f once, d from now, unless stopped first
	after func(d time.Duration, f func()) (stop func() bool)

	// since the server started
	inFlight, rate, unauthorized atomic.Uint64

	mu     sync.Mutex
	stop   func() bool // the pending summary's timer; nil when none is pending
	closed bool
	// since the last summary
	clients map[string]*clientRefusals
	unauth  int                 // unauthorized requests
	unread  int                 // of them, refused before any token was compared (tries)
	addrs   map[string]struct{} // the addresses they came from, summaryAddrs at most
	more    bool                // more addresses than addrs holds
}

// clientRefusals are one client's refusals since the last summary.
type clientRefusals struct{ inFlight, rate int }

func newRefusals(log logger.Logger) *refusals {
	return &refusals{log: log, every: SummaryEvery, after: func(d time.Duration, f func()) func() bool {
		// NOTE: a timer, not a wait on an event: the summary is owed a minute after the first
		// refusal it counts, whether or not another request ever comes. It is armed by a refusal
		// and stopped by close, so a quiet server has none.
		return time.AfterFunc(d, f).Stop
	}}
}

// limited counts a request of client refused for one of its limits: its rate, or its searches in
// flight.
func (r *refusals) limited(client string, rate bool) {
	if rate {
		r.rate.Add(1)
	} else {
		r.inFlight.Add(1)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients == nil {
		r.clients = map[string]*clientRefusals{}
	}
	c := r.clients[client]
	if c == nil {
		c = &clientRefusals{}
		r.clients[client] = c
	}
	if rate {
		c.rate++
	} else {
		c.inFlight++
	}
	r.arm()
}

// unauthorizedFrom counts a request with no valid token from addr ("" when the address says
// nothing: behind a proxy). unread is one refused before its token was compared, its address over
// its tries.
func (r *refusals) unauthorizedFrom(addr string, unread bool) {
	r.unauthorized.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unauth++
	if unread {
		r.unread++
	}
	if addr != "" {
		if r.addrs == nil {
			r.addrs = map[string]struct{}{}
		}
		if _, known := r.addrs[addr]; !known {
			if len(r.addrs) < summaryAddrs {
				r.addrs[addr] = struct{}{}
			} else {
				r.more = true
			}
		}
	}
	r.arm()
}

// arm owes a summary, every from now, unless one is owed already. r.mu is held.
func (r *refusals) arm() {
	if r.stop == nil && !r.closed {
		r.stop = r.after(r.every, r.summarise)
	}
}

// summarise says what was refused since the last summary, and starts the next count from nothing.
// The timer's goroutine calls it, and close.
func (r *refusals) summarise() {
	r.mu.Lock()
	clients, unauth, unread, addrs, more := r.clients, r.unauth, r.unread, len(r.addrs), r.more
	r.clients, r.unauth, r.unread, r.addrs, r.more, r.stop = nil, 0, 0, nil, false, nil
	r.mu.Unlock()
	for _, name := range slices.Sorted(maps.Keys(clients)) {
		c := clients[name]
		logger.Info(r.log, fmt.Sprintf("search: client %s: %d refused in %v: %d over its searches in flight (too_many_in_flight), %d over its rate (too_many_requests)",
			name, c.inFlight+c.rate, r.every, c.inFlight, c.rate))
	}
	if unauth > 0 {
		line := fmt.Sprintf("http: %d unauthorized requests in %v", unauth, r.every)
		switch {
		case more:
			line += fmt.Sprintf(", from over %d addresses", addrs)
		case addrs == 1:
			line += ", from 1 address"
		case addrs > 1:
			line += fmt.Sprintf(", from %d addresses", addrs)
		}
		if unread > 0 {
			line += fmt.Sprintf("; %d of them refused unread, their address over its tries", unread)
		}
		logger.Info(r.log, line)
	}
}

// close says what is counted and not yet said, and owes no summary after: a server stopping loses
// none. A refusal counted after it is counted, and never said.
func (r *refusals) close() {
	r.mu.Lock()
	stop := r.stop
	r.closed = true
	r.mu.Unlock()
	if stop != nil {
		stop() // whether or not it ran: summarise takes what is there, once
	}
	r.summarise()
}

// totals are the refusals since the server started, by reason.
func (r *refusals) totals() ops.Refused {
	return ops.Refused{InFlight: r.inFlight.Load(), Rate: r.rate.Load(), Unauthorized: r.unauthorized.Load()}
}

// THE TRIES OF AN ADDRESS. A token is checked by comparing its hash with every client's and every
// operator's, which anyone may ask for, as often as they like. Each unauthorized request takes one
// try from its address: 20 at once, 5 a second after. An address with none left is refused 429
// too_many_requests before any hash is compared, until a try is back.
//
// The address is the connection's own. Behind a proxy it is the proxy's, for every caller, so the
// tries are not kept there (WithoutAddressTries): the proxy has its own limit.

// The tries of an address, and how many addresses are remembered: the one longest unseen goes
// first.
const (
	triesRate  = 5.0 // a second
	triesBurst = 20
	triesAddrs = 10000
)

// tries is the unauthorized requests' budget, by address. It is safe for concurrent use.
type tries struct {
	mu     sync.Mutex
	byAddr map[string]*list.Element // of *tried
	seen   *list.List               // the most lately seen first
}

// tried is one address's tries left.
type tried struct {
	addr   string
	tokens float64
	last   time.Time
}

func newTries() *tries { return &tries{byAddr: map[string]*list.Element{}, seen: list.New()} }

// spent reports whether addr has no try left at now, and how long until it has one. An address
// never refused has every try, and is not remembered.
func (t *tries) spent(addr string, now time.Time) (wait time.Duration, spent bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, known := t.byAddr[addr]
	if !known {
		return 0, false
	}
	a := e.Value.(*tried)
	a.fill(now)
	if a.tokens >= triesBurst {
		// every try back: nothing to remember of it
		t.seen.Remove(e)
		delete(t.byAddr, addr)
		return 0, false
	}
	if a.tokens >= 1 {
		return 0, false
	}
	return time.Duration((1 - a.tokens) / triesRate * float64(time.Second)), true
}

// take takes one try from addr at now, for an unauthorized request of its own.
func (t *tries) take(addr string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, known := t.byAddr[addr]
	if !known {
		if t.seen.Len() >= triesAddrs {
			oldest := t.seen.Back()
			delete(t.byAddr, oldest.Value.(*tried).addr)
			t.seen.Remove(oldest)
		}
		e = t.seen.PushFront(&tried{addr: addr, tokens: triesBurst, last: now})
		t.byAddr[addr] = e
	} else {
		t.seen.MoveToFront(e)
	}
	a := e.Value.(*tried)
	a.fill(now)
	a.tokens = math.Max(a.tokens-1, 0)
}

// fill brings the tries up to date at now; a clock that went back adds none.
func (a *tried) fill(now time.Time) {
	if d := now.Sub(a.last); d > 0 {
		a.tokens = math.Min(triesBurst, a.tokens+d.Seconds()*triesRate)
		a.last = now
	}
}

// addressOf is the address a request came from, without its port; "" when it says none.
func addressOf(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}
