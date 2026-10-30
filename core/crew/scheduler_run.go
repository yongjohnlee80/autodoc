package crew

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errNoWaker is an endpoint of a kind no waker serves (phase 3 brings them): a failed attempt,
// backed off like any other.
var errNoWaker = errors.New("crew: no waker for the endpoint's kind")

// Scheduler is the wake scheduler (§4.7): one loop, woken by pokes (a send, an expiry, an endpoint,
// a reattach) and by a timer on the earliest deadline, reserving attempts for due sessions under
// wakePermits permits. A permit is held by an attempt's goroutine until its call returns: resetting
// a row never frees one.
type Scheduler struct {
	m       *Manager
	run     string
	wakers  map[string]Waker
	clock   func() time.Time
	report  func(error)
	permits chan struct{}
	poke    chan struct{}
	wg      sync.WaitGroup

	mu       sync.Mutex
	watchers map[chan struct{}]struct{}
}

// SchedulerOption configures a Scheduler.
type SchedulerOption func(*Scheduler)

// WithWakeClock sets the clock the scheduler's timer measures deadlines against (the store's).
func WithWakeClock(now func() time.Time) SchedulerOption { return func(s *Scheduler) { s.clock = now } }

// WithWakeErrors sets where a look's or an attempt's store errors go (the daemon's log).
func WithWakeErrors(report func(error)) SchedulerOption {
	return func(s *Scheduler) { s.report = report }
}

// NewScheduler is the wake scheduler for this daemon run (a fresh random id made once at start),
// calling the wakers by endpoint kind.
func (m *Manager) NewScheduler(run string, wakers map[string]Waker, opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{m: m, run: run, wakers: wakers, clock: time.Now, report: func(error) {},
		permits: make(chan struct{}, wakePermits), poke: make(chan struct{}, 1), watchers: map[chan struct{}]struct{}{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Poke asks the loop to look again, and tells every watcher something may have changed. It never
// blocks: a poke while one is pending is the same poke.
func (s *Scheduler) Poke() {
	select {
	case s.poke <- struct{}{}:
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Watch is a channel that hears every poke, for a long-poll (mail.wait, crew.wait): register it
// before checking, so a change committed in between still signals. stop unregisters it.
func (s *Scheduler) Watch() (ch <-chan struct{}, stop func()) {
	c := make(chan struct{}, 1)
	s.mu.Lock()
	s.watchers[c] = struct{}{}
	s.mu.Unlock()
	return c, func() {
		s.mu.Lock()
		delete(s.watchers, c)
		s.mu.Unlock()
	}
}

// Run recovers the earlier run's attempts, then loops until ctx ends, and waits for its attempts.
func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.m.RecoverRun(ctx, s.run); err != nil {
		return err
	}
	defer s.wg.Wait()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		s.Look(ctx)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		if at, has, err := s.m.nextAt(ctx); err != nil {
			s.report(err)
		} else if has {
			timer.Reset(max(at.Sub(s.clock()), 0))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.poke:
		case <-timer.C:
		}
	}
}

// Look ends the timed-out waits, then reserves an attempt for each due session while permits last,
// and starts each one's call. It returns once they are started; Wait waits for their calls.
func (s *Scheduler) Look(ctx context.Context) {
	due, err := s.m.sweep(ctx)
	if err != nil {
		s.report(err)
		return
	}
	for _, session := range due {
		select {
		case s.permits <- struct{}{}:
		default:
			return // none free: a returning call pokes the loop
		}
		tok, ep, ok, err := s.m.Reserve(ctx, session, s.run)
		if err != nil || !ok {
			if err != nil {
				s.report(err)
			}
			<-s.permits
			continue
		}
		s.wg.Add(1)
		go s.attempt(ctx, session, tok, ep)
	}
}

// Wait waits for every started attempt's call to return.
func (s *Scheduler) Wait() { s.wg.Wait() }

func (s *Scheduler) attempt(ctx context.Context, session, tok string, ep Endpoint) {
	defer func() { <-s.permits; s.wg.Done(); s.Poke() }() // the permit returns only when this call does
	current, err := s.m.StillCurrent(ctx, session, tok, ep.Generation)
	if err != nil || !current {
		if err != nil {
			s.report(err)
		}
		if err := s.m.Abandon(ctx, session, tok); err != nil {
			s.report(err)
		}
		return
	}
	werr := errNoWaker
	if w := s.wakers[ep.Kind]; w != nil {
		cctx, cancel := context.WithTimeout(ctx, wakeCallLimit)
		werr = w.Wake(cctx, ep, WakeNotice)
		cancel()
	}
	if err := s.m.Outcome(ctx, session, tok, classify(werr)); err != nil {
		s.report(err)
	}
}
