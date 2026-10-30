package crew

import (
	"context"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/errs"
)

// The long-polls (ADR 1793271500 §4.6): crew.wait (a host's), mail.wait (an agent's) and
// inbox.watch (a window's). Each registers on the wake signal before it checks, re-checks only when
// that signal or a deadline fires, and holds no store transaction while it waits.

// MaxWait is the longest a long-poll waits for a change.
const MaxWait = 25 * time.Second

// CodeLongPollsBusy is the daemon's shared code (rpc's CodeLongPollsBusy) for a third long-poll on
// one connection. Every module that serves a long-poll answers it the same way.
const CodeLongPollsBusy int64 = -32064

// ErrLongPollsBusy is a long-poll refused because its connection already holds MaxLongPolls.
var ErrLongPollsBusy = errs.Sentinel(errs.ErrPrecondition, "crew: the connection holds its long-polls already")

// MaxLongPolls is the long-polls one connection may hold at once (§4.6): a host holds crew.wait
// and inbox.watch, an agent one mail.wait. A third is refused, so normal calls always keep the
// connection's handler slots.
const MaxLongPolls = 2

// LongPolls counts each connection's long-polls in flight, by its client token, across the modules
// that serve them. A nil *LongPolls caps nothing.
type LongPolls struct {
	mu   sync.Mutex
	held map[string]int
}

// NewLongPolls is a limiter with no long-poll in flight.
func NewLongPolls() *LongPolls { return &LongPolls{held: map[string]int{}} }

// Enter admits one long-poll on client's connection, or answers ErrLongPollsBusy; leave ends it.
// A caller with no client token (no host set one) is not counted.
func (l *LongPolls) Enter(client string) (leave func(), err error) {
	if l == nil || client == "" {
		return func() {}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[client] >= MaxLongPolls {
		return nil, ErrLongPollsBusy
	}
	l.held[client]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.held[client]--; l.held[client] <= 0 {
				delete(l.held, client)
			}
		})
	}, nil
}

// Watch is a channel that hears every change a long-poll may be waiting for: the scheduler's pokes,
// which every committed write that changed a wake row sends (Signals.Wake). Register it before
// checking, so a change committed in between still signals; stop unregisters it. Before the
// daemon's scheduler exists, the channel never fires and a long-poll waits for its deadlines.
func (m *Manager) Watch() (ch <-chan struct{}, stop func()) {
	if s := m.sched.Load(); s != nil {
		return s.Watch()
	}
	return nil, func() {}
}

// Poke tells the daemon's scheduler, and through it every long-poll's Watch, that a wake row
// changed (Signals.Wake). Before the scheduler exists it does nothing.
func (m *Manager) Poke() {
	if s := m.sched.Load(); s != nil {
		s.Poke()
	}
}

// Pending is one of a host's sessions with mail waiting: Unhandled counts its profile's queued and
// leased deliveries (a leased one the agent has seen, and not finished).
type Pending struct {
	Session   string
	Unhandled int
}

// Waited is crew.wait's answer: the sessions with mail waiting, or that the wait timed out.
type Waited struct {
	Ready    []Pending
	TimedOut bool
}

// Wait is crew.wait (§4.4): one long-poll for a host's tier-3 sessions, answering as soon as any of
// sessions (the host's own, live under epoch) has mail it may receive, unbound or bound to it, or at
// wait (at most MaxWait). Each check is one write, which guards the host, runs mail's expiry and
// reads each session's eligibility; between checks it holds no transaction, and it checks again
// only when the wake signal fires or mail's next deadline passes (a lapsing lease makes mail
// eligible again).
func (m *Manager) Wait(ctx context.Context, scope, run string, epoch int64, sessions []string, wait time.Duration) (Waited, error) {
	wait = min(max(wait, 0), MaxWait)
	heard, stop := m.Watch() // before the check: a change committed in between is heard
	defer stop()
	// NOTE: timed by the caller's long-poll: crew.wait answers "timed out" once wait passes, so the
	// host's loop comes back to renew and re-ask; a change or ctx ends the wait sooner.
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		var out Waited
		var next time.Duration
		err := m.store.Write(ctx, func(tx Tx) error {
			out, next = Waited{}, -1
			if err := guardHost(tx, scope, run, epoch); err != nil {
				return err
			}
			if m.ender == nil {
				return nil
			}
			if err := m.ender.Expire(tx); err != nil {
				return err
			}
			for _, id := range sessions {
				s, ok, err := tx.Session(id)
				if err != nil {
					return err
				}
				if !ok || s.Ended != nil || s.Host != scope || s.Epoch != epoch {
					continue // not this host's live session: nothing it may wait on
				}
				ready, err := m.ender.EligibleFor(tx, s.Profile, s.ID) // its own bound mail too
				if err != nil || !ready {
					if err != nil {
						return err
					}
					continue
				}
				n, err := m.ender.UnhandledCount(tx, s.Profile)
				if err != nil {
					return err
				}
				out.Ready = append(out.Ready, Pending{Session: s.ID, Unhandled: n})
			}
			at, has, err := m.ender.NextDeadline(tx)
			if err == nil && has {
				next = max(time.Duration(at-tx.Now().UnixNano()), 0)
			}
			return err
		})
		if err != nil || len(out.Ready) > 0 {
			return out, err
		}
		if timedOut, err := WaitFor(ctx, heard, deadline.C, next); err != nil || timedOut {
			return Waited{TimedOut: timedOut}, err
		}
	}
}

// WaitFor waits between a long-poll's checks, for the wake signal heard, the long-poll's own
// deadline, mail's next deadline (next; negative is none) or ctx, and answers whether the
// long-poll's deadline ended it. Every long-poll's loop uses it, so each re-checks only when one of
// them fires, never in a loop of its own.
func WaitFor(ctx context.Context, heard <-chan struct{}, deadline <-chan time.Time, next time.Duration) (timedOut bool, err error) {
	var nextC <-chan time.Time
	if next >= 0 {
		// NOTE: mail changes by time alone (a lease lapsing, a message expiring) with no write to
		// signal it: the wait re-checks at that instant, when the check's own expire makes it real.
		t := time.NewTimer(next)
		defer t.Stop()
		nextC = t.C
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-deadline:
		return true, nil
	case <-heard:
	case <-nextC:
	}
	return false, nil
}
